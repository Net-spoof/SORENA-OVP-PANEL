package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

const (
	openVPNPortLocationsPath = "/opt/irangate/routing/openvpn-port-locations.json"

	openVPNPortLocationsRuntime = "/usr/local/sbin/irangate-openvpn-port-locations"
)

var (
	openVPNPortLocationsMutex sync.Mutex

	openVPNPortLocationIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)
)

type OpenVPNPortLocation struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Flag           string `json:"flag,omitempty"`
	Location       string `json:"location,omitempty"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Protocol       string `json:"protocol"`
	Interface      string `json:"interface"`
	Subnet         string `json:"subnet"`
	ManagementPort int    `json:"management_port"`
	StatusFile     string `json:"status_file"`
	TProxyPort     int    `json:"tproxy_port"`
	Enabled        bool   `json:"enabled"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type OpenVPNPortLocationsFile struct {
	Version   int                   `json:"version"`
	Locations []OpenVPNPortLocation `json:"locations"`
}

// IRANGATE_PORT_LOCATION_RUNTIME_VIEW_STAGE8C
type OpenVPNPortLocationView struct {
	OpenVPNPortLocation
	Runtime map[string]interface{} `json:"runtime"`
}

func normalizeOpenVPNPortLocationID(value string) string {
	value = strings.ToLower(
		strings.TrimSpace(value),
	)

	value = strings.ReplaceAll(value, "_", "-")
	value = strings.Trim(value, "-")

	return value
}

func loadOpenVPNPortLocations() (
	OpenVPNPortLocationsFile,
	error,
) {
	result := OpenVPNPortLocationsFile{
		Version:   1,
		Locations: []OpenVPNPortLocation{},
	}

	data, err := os.ReadFile(
		openVPNPortLocationsPath,
	)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}

		return result, err
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return result, nil
	}

	if err := json.Unmarshal(
		data,
		&result,
	); err != nil {
		return result, err
	}

	if result.Version <= 0 {
		result.Version = 1
	}

	if result.Locations == nil {
		result.Locations =
			[]OpenVPNPortLocation{}
	}

	return result, nil
}

func saveOpenVPNPortLocations(
	data OpenVPNPortLocationsFile,
) error {
	data.Version = 1

	if data.Locations == nil {
		data.Locations =
			[]OpenVPNPortLocation{}
	}

	if err := os.MkdirAll(
		filepath.Dir(
			openVPNPortLocationsPath,
		),
		0755,
	); err != nil {
		return err
	}

	payload, err := json.MarshalIndent(
		data,
		"",
		"  ",
	)
	if err != nil {
		return err
	}

	tempPath :=
		openVPNPortLocationsPath +
			".tmp"

	if err := os.WriteFile(
		tempPath,
		payload,
		0600,
	); err != nil {
		return err
	}

	if err := os.Rename(
		tempPath,
		openVPNPortLocationsPath,
	); err != nil {
		return err
	}

	return os.Chmod(
		openVPNPortLocationsPath,
		0600,
	)
}

func findOpenVPNPortLocation(
	id string,
) (
	OpenVPNPortLocation,
	bool,
) {
	id = normalizeOpenVPNPortLocationID(id)

	data, err :=
		loadOpenVPNPortLocations()
	if err != nil {
		return OpenVPNPortLocation{}, false
	}

	for _, location := range data.Locations {

		if location.ID == id {
			return location, true
		}
	}

	return OpenVPNPortLocation{}, false
}

func openVPNPortLocationSelected(
	clientData map[string]interface{},
	id string,
) bool {
	id = normalizeOpenVPNPortLocationID(id)
	if id == "" {
		return false
	}

	raw, exists :=
		clientData["openvpn_port_locations"]

	if !exists {
		return false
	}

	switch values := raw.(type) {
	case []interface{}:
		for _, value := range values {
			if normalizeOpenVPNPortLocationID(
				fmt.Sprintf("%v", value),
			) == id {
				return true
			}
		}

	case []string:
		for _, value := range values {
			if normalizeOpenVPNPortLocationID(
				value,
			) == id {
				return true
			}
		}
	}

	return false
}

func nextOpenVPNPortLocationSlot(
	data OpenVPNPortLocationsFile,
) (int, error) {
	used := make(map[int]bool)

	for _, location := range data.Locations {

		if location.ManagementPort >= 7511 &&
			location.ManagementPort <= 7710 {

			slot :=
				location.ManagementPort -
					7510

			used[slot] = true
		}
	}

	for slot := 1; slot <= 200; slot++ {
		if !used[slot] {
			return slot, nil
		}
	}

	return 0, fmt.Errorf(
		"maximum OpenVPN port locations reached",
	)
}

func subnetOverlaps(
	left string,
	right string,
) bool {
	leftIP, leftNet, leftErr :=
		net.ParseCIDR(left)

	rightIP, rightNet, rightErr :=
		net.ParseCIDR(right)

	if leftErr != nil || rightErr != nil {
		return false
	}

	return leftNet.Contains(rightIP) ||
		rightNet.Contains(leftIP)
}

func validateOpenVPNPortLocation(
	location OpenVPNPortLocation,
	existingID string,
	data OpenVPNPortLocationsFile,
) error {
	location.ID =
		normalizeOpenVPNPortLocationID(
			location.ID,
		)

	existingID =
		normalizeOpenVPNPortLocationID(
			existingID,
		)

	if !openVPNPortLocationIDPattern.
		MatchString(location.ID) {

		return fmt.Errorf(
			"ID must contain lowercase letters, numbers or hyphens",
		)
	}

	if strings.TrimSpace(location.Name) == "" {
		return fmt.Errorf(
			"name is required",
		)
	}

	if strings.TrimSpace(location.Host) == "" {
		return fmt.Errorf(
			"host is required",
		)
	}

	if strings.Contains(
		location.Host,
		"://",
	) ||
		strings.ContainsAny(
			location.Host,
			" \t\r\n/",
		) {
		return fmt.Errorf(
			"invalid public host",
		)
	}

	if location.Port < 1 ||
		location.Port > 65535 {

		return fmt.Errorf(
			"port must be between 1 and 65535",
		)
	}

	if location.Port == 500 ||
		location.Port == 4500 {

		return fmt.Errorf(
			"UDP 500 and 4500 are reserved for IKEv2",
		)
	}

	if strings.ToLower(
		strings.TrimSpace(
			location.Protocol,
		),
	) != "udp" {

		return fmt.Errorf(
			"only UDP is supported",
		)
	}

	if _, network, err :=
		net.ParseCIDR(location.Subnet); err != nil {

		return fmt.Errorf(
			"invalid subnet: %w",
			err,
		)
	} else {
		ones, bits :=
			network.Mask.Size()

		if bits != 32 || ones != 24 {
			return fmt.Errorf(
				"subnet must be IPv4 /24",
			)
		}
	}

	if subnetOverlaps(
		location.Subnet,
		"10.8.0.0/24",
	) {
		return fmt.Errorf(
			"subnet overlaps main OpenVPN subnet",
		)
	}

	if subnetOverlaps(
		location.Subnet,
		"10.10.0.0/24",
	) {
		return fmt.Errorf(
			"subnet overlaps IKEv2 subnet",
		)
	}

	if location.ManagementPort < 1024 ||
		location.ManagementPort > 65535 {

		return fmt.Errorf(
			"invalid management port",
		)
	}

	if location.TProxyPort < 1024 ||
		location.TProxyPort > 65535 {

		return fmt.Errorf(
			"invalid TPROXY port",
		)
	}

	for _, current := range data.Locations {

		if current.ID == existingID {
			continue
		}

		if current.ID == location.ID {
			return fmt.Errorf(
				"location ID already exists",
			)
		}

		if current.Port == location.Port {
			return fmt.Errorf(
				"OpenVPN port is already in use",
			)
		}

		if current.Interface ==
			location.Interface {

			return fmt.Errorf(
				"interface is already in use",
			)
		}

		if current.ManagementPort ==
			location.ManagementPort {

			return fmt.Errorf(
				"management port is already in use",
			)
		}

		if current.TProxyPort ==
			location.TProxyPort {

			return fmt.Errorf(
				"TPROXY port is already in use",
			)
		}

		if subnetOverlaps(
			current.Subnet,
			location.Subnet,
		) {
			return fmt.Errorf(
				"subnet overlaps location %s",
				current.ID,
			)
		}
	}

	return nil
}

func applyOpenVPNPortLocationsRuntime() error {
	if _, err := os.Stat(
		openVPNPortLocationsRuntime,
	); err != nil {

		if os.IsNotExist(err) {
			return nil
		}

		return err
	}

	cmd := exec.Command(
		openVPNPortLocationsRuntime,
		"apply",
	)

	output, err :=
		cmd.CombinedOutput()

	if err != nil {
		return fmt.Errorf(
			"runtime apply failed: %w: %s",
			err,
			strings.TrimSpace(
				string(output),
			),
		)
	}

	return nil
}

func getOpenVPNPortLocationsHandler(
	w http.ResponseWriter,
	_ *http.Request,
) {
	data, err :=
		loadOpenVPNPortLocations()

	if err != nil {
		sendError(
			w,
			"Failed to load OpenVPN port locations: "+
				err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	locations :=
		make(
			[]OpenVPNPortLocationView,
			0,
			len(data.Locations),
		)

	for _, location := range data.Locations {

		locations = append(
			locations,
			OpenVPNPortLocationView{
				OpenVPNPortLocation: location,
				Runtime: openVPNPortLocationRuntimeStatus(
					location,
				),
			},
		)
	}

	sendSuccess(
		w,
		"OpenVPN port locations retrieved",
		locations,
	)
}

func createOpenVPNPortLocationHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	var location OpenVPNPortLocation

	if err := json.NewDecoder(
		r.Body,
	).Decode(&location); err != nil {

		sendError(
			w,
			"Invalid request body",
			http.StatusBadRequest,
		)
		return
	}

	openVPNPortLocationsMutex.Lock()
	defer openVPNPortLocationsMutex.Unlock()

	data, err :=
		loadOpenVPNPortLocations()

	if err != nil {
		sendError(
			w,
			"Failed to load OpenVPN port locations: "+
				err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	location.ID =
		normalizeOpenVPNPortLocationID(
			location.ID,
		)

	location.Name =
		strings.TrimSpace(location.Name)

	location.Flag =
		strings.TrimSpace(location.Flag)

	location.Location =
		strings.ToUpper(
			strings.TrimSpace(
				location.Location,
			),
		)

	location.Host =
		strings.TrimSpace(location.Host)

	location.Protocol = "udp"

	slot, err :=
		nextOpenVPNPortLocationSlot(data)

	if err != nil {
		sendError(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	location.Interface =
		fmt.Sprintf(
			"tunpl%03d",
			slot,
		)

	location.Subnet =
		fmt.Sprintf(
			"10.20.%d.0/24",
			slot,
		)

	location.ManagementPort =
		7510 + slot

	location.TProxyPort =
		12400 + slot

	location.StatusFile =
		fmt.Sprintf(
			"/etc/openvpn/openvpn-status-port-%s.log",
			location.ID,
		)

	location.Enabled = true

	now :=
		time.Now().
			UTC().
			Format(time.RFC3339)

	location.CreatedAt = now
	location.UpdatedAt = now

	if location.Host == "" {
		if host, hostErr :=
			configuredOpenVPNDownloadHost(); hostErr == nil {

			location.Host = host
		}
	}

	if err := validateOpenVPNPortLocation(
		location,
		"",
		data,
	); err != nil {
		sendError(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	data.Locations = append(
		data.Locations,
		location,
	)

	if err := saveOpenVPNPortLocations(
		data,
	); err != nil {
		sendError(
			w,
			"Failed to save OpenVPN port location: "+
				err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	runtimeErr :=
		applyOpenVPNPortLocationsRuntime()

	response := map[string]interface{}{
		"location": location,
		"applied":  runtimeErr == nil,
	}

	if runtimeErr != nil {
		response["runtime_error"] =
			runtimeErr.Error()
	}

	sendSuccess(
		w,
		"OpenVPN port location created",
		response,
	)
}

func updateOpenVPNPortLocationHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	id :=
		normalizeOpenVPNPortLocationID(
			mux.Vars(r)["id"],
		)

	if id == "" {
		sendError(
			w,
			"Location ID is required",
			http.StatusBadRequest,
		)
		return
	}

	var patch OpenVPNPortLocation

	if err := json.NewDecoder(
		r.Body,
	).Decode(&patch); err != nil {

		sendError(
			w,
			"Invalid request body",
			http.StatusBadRequest,
		)
		return
	}

	openVPNPortLocationsMutex.Lock()
	defer openVPNPortLocationsMutex.Unlock()

	data, err :=
		loadOpenVPNPortLocations()

	if err != nil {
		sendError(
			w,
			"Failed to load OpenVPN port locations: "+
				err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	index := -1

	for currentIndex := range data.Locations {

		if data.Locations[currentIndex].ID == id {
			index = currentIndex
			break
		}
	}

	if index < 0 {
		sendError(
			w,
			"OpenVPN port location not found",
			http.StatusNotFound,
		)
		return
	}

	current := data.Locations[index]

	if strings.TrimSpace(patch.Name) != "" {
		current.Name =
			strings.TrimSpace(patch.Name)
	}

	if strings.TrimSpace(patch.Flag) != "" {
		current.Flag =
			strings.TrimSpace(patch.Flag)
	}

	if strings.TrimSpace(
		patch.Location,
	) != "" {
		current.Location =
			strings.ToUpper(
				strings.TrimSpace(
					patch.Location,
				),
			)
	}

	if strings.TrimSpace(patch.Host) != "" {
		current.Host =
			strings.TrimSpace(patch.Host)
	}

	if patch.Port > 0 {
		current.Port = patch.Port
	}

	current.Enabled = patch.Enabled
	current.Protocol = "udp"

	current.UpdatedAt =
		time.Now().
			UTC().
			Format(time.RFC3339)

	if err := validateOpenVPNPortLocation(
		current,
		id,
		data,
	); err != nil {
		sendError(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	data.Locations[index] = current

	if err := saveOpenVPNPortLocations(
		data,
	); err != nil {
		sendError(
			w,
			"Failed to save OpenVPN port location: "+
				err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	runtimeErr :=
		applyOpenVPNPortLocationsRuntime()

	response := map[string]interface{}{
		"location": current,
		"applied":  runtimeErr == nil,
	}

	if runtimeErr != nil {
		response["runtime_error"] =
			runtimeErr.Error()
	}

	sendSuccess(
		w,
		"OpenVPN port location updated",
		response,
	)
}

func deleteOpenVPNPortLocationHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	id :=
		normalizeOpenVPNPortLocationID(
			mux.Vars(r)["id"],
		)

	if id == "" {
		sendError(
			w,
			"Location ID is required",
			http.StatusBadRequest,
		)
		return
	}

	openVPNPortLocationsMutex.Lock()
	defer openVPNPortLocationsMutex.Unlock()

	data, err :=
		loadOpenVPNPortLocations()

	if err != nil {
		sendError(
			w,
			"Failed to load OpenVPN port locations: "+
				err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	next :=
		make(
			[]OpenVPNPortLocation,
			0,
			len(data.Locations),
		)

	found := false

	for _, location := range data.Locations {

		if location.ID == id {
			found = true
			continue
		}

		next = append(
			next,
			location,
		)
	}

	if !found {
		sendError(
			w,
			"OpenVPN port location not found",
			http.StatusNotFound,
		)
		return
	}

	data.Locations = next

	if err := saveOpenVPNPortLocations(
		data,
	); err != nil {
		sendError(
			w,
			"Failed to save OpenVPN port locations: "+
				err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	runtimeErr :=
		applyOpenVPNPortLocationsRuntime()

	response := map[string]interface{}{
		"id":      id,
		"applied": runtimeErr == nil,
	}

	if runtimeErr != nil {
		response["runtime_error"] =
			runtimeErr.Error()
	}

	sendSuccess(
		w,
		"OpenVPN port location deleted",
		response,
	)
}

func rewriteOpenVPNProfileEndpoint(
	profile []byte,
	host string,
	port int,
	protocol string,
) ([]byte, int, error) {
	host = strings.TrimSpace(host)

	if host == "" ||
		strings.Contains(host, "://") ||
		strings.ContainsAny(
			host,
			" \t\r\n/",
		) {
		return nil, 0, fmt.Errorf(
			"invalid OpenVPN public host",
		)
	}

	if port < 1 || port > 65535 {
		return nil, 0, fmt.Errorf(
			"invalid OpenVPN port",
		)
	}

	protocol =
		strings.ToLower(
			strings.TrimSpace(protocol),
		)

	if protocol != "udp" {
		return nil, 0, fmt.Errorf(
			"unsupported OpenVPN protocol",
		)
	}

	lines :=
		strings.Split(
			string(profile),
			"\n",
		)

	updated := 0

	for index, line := range lines {

		trimmed :=
			strings.TrimLeft(
				line,
				" \t",
			)

		fields :=
			strings.Fields(trimmed)

		if len(fields) == 0 {
			continue
		}

		indent :=
			line[:len(line)-len(trimmed)]

		switch strings.ToLower(
			fields[0],
		) {
		case "remote":
			lines[index] =
				fmt.Sprintf(
					"%sremote %s %d %s",
					indent,
					host,
					port,
					protocol,
				)

			updated++

		case "proto":
			lines[index] =
				indent +
					"proto " +
					protocol
		}
	}

	if updated == 0 {
		return nil, 0, fmt.Errorf(
			"OpenVPN profile has no remote directive",
		)
	}

	return []byte(
		strings.Join(
			lines,
			"\n",
		),
	), updated, nil
}

func subscriptionPortLocationDownloadHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	vars := mux.Vars(r)

	token :=
		strings.TrimSpace(
			vars["token"],
		)

	locationID :=
		normalizeOpenVPNPortLocationID(
			vars["location"],
		)

	clientName, clientData, err :=
		findClientBySubscriptionToken(
			token,
		)

	if err != nil {
		http.Error(
			w,
			"Subscription not found",
			http.StatusNotFound,
		)
		return
	}

	if !openVPNPortLocationSelected(
		clientData,
		locationID,
	) {
		http.Error(
			w,
			"Port location not selected for this client",
			http.StatusNotFound,
		)
		return
	}

	location, found :=
		findOpenVPNPortLocation(
			locationID,
		)

	if !found || !location.Enabled {
		http.Error(
			w,
			"OpenVPN port location not found",
			http.StatusNotFound,
		)
		return
	}

	profileCandidates := []string{
		filepath.Join(
			"/etc/openvpn/client",
			clientName+".ovpn",
		),
		filepath.Join(
			"/opt/irangate/clients",
			clientName+".ovpn",
		),
	}

	var profile []byte
	var profileErr error

	for _, profilePath := range profileCandidates {

		profile, profileErr =
			os.ReadFile(profilePath)

		if profileErr == nil {
			break
		}
	}

	if profileErr != nil {
		http.Error(
			w,
			"OpenVPN profile not found",
			http.StatusNotFound,
		)
		return
	}

	profile, updated, err :=
		rewriteOpenVPNProfileEndpoint(
			profile,
			location.Host,
			location.Port,
			location.Protocol,
		)

	if err != nil {
		http.Error(
			w,
			"Failed to rewrite OpenVPN profile: "+
				err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	safeName :=
		strings.NewReplacer(
			"\\", "_",
			"\"", "_",
			"\r", "_",
			"\n", "_",
		).Replace(clientName)

	safeLocation :=
		strings.NewReplacer(
			"\\", "_",
			"\"", "_",
			"\r", "_",
			"\n", "_",
		).Replace(location.ID)

	writeOpenVPNProfile(
		w,
		safeName+"-"+safeLocation+".ovpn",
		profile,
	)

	_ = updated
}

func applyOpenVPNPortLocationsHandler(
	w http.ResponseWriter,
	_ *http.Request,
) {
	if err :=
		applyOpenVPNPortLocationsRuntime(); err != nil {

		sendError(
			w,
			err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	sendSuccess(
		w,
		"OpenVPN port locations applied",
		nil,
	)
}

func openVPNPortLocationRuntimeStatus(
	location OpenVPNPortLocation,
) map[string]interface{} {
	service :=
		"openvpn@server-" +
			location.ID

	output, err :=
		exec.Command(
			"systemctl",
			"is-active",
			service,
		).CombinedOutput()

	status :=
		strings.TrimSpace(
			string(output),
		)

	if status == "" {
		status = "unknown"
	}

	return map[string]interface{}{
		"id":              location.ID,
		"service":         service,
		"status":          status,
		"active":          err == nil && status == "active",
		"port":            location.Port,
		"interface":       location.Interface,
		"subnet":          location.Subnet,
		"management_port": location.ManagementPort,
		"tproxy_port":     location.TProxyPort,
		"status_file":     location.StatusFile,
		"configured":      location.Enabled,
		"checked_at_unix": strconv.FormatInt(time.Now().Unix(), 10),
	}
}

// IRANGATE_OPENVPN_PORT_LOCATIONS_STAGE2

func openVPNManagementPorts() []int {
	ports := []int{7505}
	seen := map[int]bool{
		7505: true,
	}

	data, err := loadOpenVPNPortLocations()
	if err != nil {
		return ports
	}

	for _, location := range data.Locations {
		if !location.Enabled {
			continue
		}

		port := location.ManagementPort

		if port < 1 ||
			port > 65535 ||
			seen[port] {

			continue
		}

		seen[port] = true

		ports = append(
			ports,
			port,
		)
	}

	return ports
}

func openVPNLocalServiceNames() []string {
	services := []string{
		"openvpn@server.service",
	}

	seen := map[string]bool{
		"openvpn@server.service": true,
	}

	data, err := loadOpenVPNPortLocations()
	if err != nil {
		return services
	}

	for _, location := range data.Locations {
		if !location.Enabled {
			continue
		}

		id := normalizeOpenVPNPortLocationID(
			location.ID,
		)

		if id == "" {
			continue
		}

		service :=
			"openvpn@server-" +
				id +
				".service"

		if seen[service] {
			continue
		}

		seen[service] = true

		services = append(
			services,
			service,
		)
	}

	return services
}

func sendOpenVPNManagementCommand(
	port int,
	command string,
) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf(
			"invalid OpenVPN management port: %d",
			port,
		)
	}

	address := net.JoinHostPort(
		"127.0.0.1",
		strconv.Itoa(port),
	)

	conn, err := net.DialTimeout(
		"tcp",
		address,
		2*time.Second,
	)
	if err != nil {
		return err
	}

	defer conn.Close()

	_ = conn.SetDeadline(
		time.Now().Add(
			4 * time.Second,
		),
	)

	command =
		strings.TrimSpace(
			command,
		)

	if command == "" {
		return fmt.Errorf(
			"empty management command",
		)
	}

	if _, err := conn.Write(
		[]byte(
			command +
				"\nquit\n",
		),
	); err != nil {
		return err
	}

	return nil
}

func killOpenVPNClientEverywhere(
	name string,
) error {
	name = strings.TrimSpace(name)

	if !validNodeACLClientName(name) {
		return fmt.Errorf(
			"invalid OpenVPN client name",
		)
	}

	ports := openVPNManagementPorts()

	successCount := 0
	failures := make(
		[]string,
		0,
	)

	for _, port := range ports {
		err := sendOpenVPNManagementCommand(
			port,
			"kill "+name,
		)

		if err != nil {
			failures = append(
				failures,
				fmt.Sprintf(
					"%d: %v",
					port,
					err,
				),
			)

			continue
		}

		successCount++
	}

	// IRANGATE_KILL_EVERYWHERE_COMPLETE_STAGE7C
	if len(failures) > 0 {
		return fmt.Errorf(
			"incomplete disconnect across OpenVPN instances: "+
				"successful=%d expected=%d failures=%s",
			successCount,
			len(ports),
			strings.Join(
				failures,
				"; ",
			),
		)
	}

	return nil
}

func reloadAllLocalOpenVPNInstances() error {
	services :=
		openVPNLocalServiceNames()

	successCount := 0
	failures := make(
		[]string,
		0,
	)

	for _, service := range services {
		output, err :=
			exec.Command(
				"systemctl",
				"reload",
				service,
			).CombinedOutput()

		if err != nil {
			failures = append(
				failures,
				fmt.Sprintf(
					"%s: %v: %s",
					service,
					err,
					strings.TrimSpace(
						string(output),
					),
				),
			)

			continue
		}

		successCount++
	}

	if successCount == 0 {
		return fmt.Errorf(
			"failed to reload local OpenVPN instances: %s",
			strings.Join(
				failures,
				"; ",
			),
		)
	}

	return nil
}

// IRANGATE_OPENVPN_PORT_LOCATIONS_STAGE3

func openVPNPortLocationIDsFromClientData(
	clientData map[string]interface{},
) []string {
	result := make(
		[]string,
		0,
	)

	raw, exists :=
		clientData["openvpn_port_locations"]

	if !exists {
		return result
	}

	seen := make(
		map[string]bool,
	)

	appendID := func(value string) {
		id :=
			normalizeOpenVPNPortLocationID(
				value,
			)

		if id == "" || seen[id] {
			return
		}

		seen[id] = true

		result = append(
			result,
			id,
		)
	}

	switch values := raw.(type) {
	case []interface{}:
		for _, value := range values {
			appendID(
				fmt.Sprintf(
					"%v",
					value,
				),
			)
		}

	case []string:
		for _, value := range values {
			appendID(value)
		}
	}

	return result
}

func validateOpenVPNPortLocationSelection(
	rawIDs []string,
) ([]string, error) {
	data, err :=
		loadOpenVPNPortLocations()

	if err != nil {
		return nil, fmt.Errorf(
			"load OpenVPN port locations: %w",
			err,
		)
	}

	allowed := make(
		map[string]bool,
	)

	for _, location := range data.Locations {

		id :=
			normalizeOpenVPNPortLocationID(
				location.ID,
			)

		if id == "" || !location.Enabled {
			continue
		}

		allowed[id] = true
	}

	result := make(
		[]string,
		0,
		len(rawIDs),
	)

	seen := make(
		map[string]bool,
	)

	for _, rawID := range rawIDs {
		id :=
			normalizeOpenVPNPortLocationID(
				rawID,
			)

		if id == "" {
			continue
		}

		if !allowed[id] {
			return nil, fmt.Errorf(
				"invalid or disabled OpenVPN port location: %s",
				rawID,
			)
		}

		if seen[id] {
			continue
		}

		seen[id] = true

		result = append(
			result,
			id,
		)
	}

	return result, nil
}

func subscriptionOpenVPNPortLocationDownloads(
	clientData map[string]interface{},
	token string,
	cacheVersion string,
) []map[string]string {
	result := make(
		[]map[string]string,
		0,
	)

	selected := make(
		map[string]bool,
	)

	for _, id := range openVPNPortLocationIDsFromClientData(
		clientData,
	) {

		selected[id] = true
	}

	if len(selected) == 0 {
		return result
	}

	data, err :=
		loadOpenVPNPortLocations()

	if err != nil {
		return result
	}

	for _, location := range data.Locations {

		id :=
			normalizeOpenVPNPortLocationID(
				location.ID,
			)

		if id == "" ||
			!location.Enabled ||
			!selected[id] {

			continue
		}

		result = append(
			result,
			map[string]string{
				"ID":       id,
				"Name":     location.Name,
				"Flag":     location.Flag,
				"Location": location.Location,
				"Port": strconv.Itoa(
					location.Port,
				),
				"URL": "/sub/" +
					url.PathEscape(token) +
					"/port/" +
					url.PathEscape(id) +
					"/download?v=" +
					url.QueryEscape(
						cacheVersion,
					),
			},
		)
	}

	return result
}
