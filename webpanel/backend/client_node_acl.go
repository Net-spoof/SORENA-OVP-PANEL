package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const localNodeDeniedDir = "/etc/openvpn/node-denied-clients"

func nodeIDsFromClientData(
	clientData map[string]interface{},
) []string {
	result := []string{}

	raw, exists := clientData["nodes"]

	if !exists {
		return result
	}

	seen := map[string]bool{}

	switch values := raw.(type) {

	case []interface{}:
		for _, value := range values {
			id := normalizeNodeID(
				fmt.Sprintf("%v", value),
			)

			if id == "" || seen[id] {
				continue
			}

			seen[id] = true

			result = append(
				result,
				id,
			)
		}

	case []string:
		for _, value := range values {
			id := normalizeNodeID(value)

			if id == "" || seen[id] {
				continue
			}

			seen[id] = true

			result = append(
				result,
				id,
			)
		}
	}

	return result
}

func validNodeACLClientName(
	name string,
) bool {
	if name == "" {
		return false
	}

	if filepath.Base(name) != name {
		return false
	}

	if strings.ContainsAny(
		name,
		"/\\\x00\r\n",
	) {
		return false
	}

	return true
}

func killLocalNodeClient(
	name string,
) {
	cmd := exec.Command(
		"nc",
		"-w",
		"2",
		"127.0.0.1",
		"7505",
	)

	cmd.Stdin = strings.NewReader(
		"kill " +
			name +
			"\nquit\n",
	)

	_ = cmd.Run()
}

func setLocalNodeAccess(
	name string,
	allowed bool,
) error {
	if !validNodeACLClientName(name) {
		return fmt.Errorf(
			"invalid client name",
		)
	}

	if err := os.MkdirAll(
		localNodeDeniedDir,
		0755,
	); err != nil {
		return err
	}

	path := filepath.Join(
		localNodeDeniedDir,
		name,
	)

	if allowed {
		err := os.Remove(path)

		if err != nil &&
			!os.IsNotExist(err) {
			return err
		}

		return nil
	}

	if err := os.WriteFile(
		path,
		[]byte("denied\n"),
		0644,
	); err != nil {
		return err
	}

	killLocalNodeClient(name)

	return nil
}

func nodeControlEndpoint(
	node NodeConfig,
	action string,
	name string,
) (
	string,
	error,
) {
	host := strings.TrimSpace(
		node.PublicIP,
	)

	if host == "" {
		parsed, err := url.Parse(
			node.AgentURL,
		)

		if err != nil {
			return "", err
		}

		host = parsed.Hostname()
	}

	if host == "" {
		return "", fmt.Errorf(
			"node %s has no reachable host",
			node.ID,
		)
	}

	endpoint := url.URL{
		Scheme: "http",
		Host: net.JoinHostPort(
			host,
			"5024",
		),
		Path: action,
	}

	query := endpoint.Query()

	query.Set(
		"name",
		name,
	)

	endpoint.RawQuery =
		query.Encode()

	return endpoint.String(), nil
}

func setRemoteNodeAccess(
	node NodeConfig,
	name string,
	allowed bool,
) error {
	action := "/deny-node-client"

	if allowed {
		action = "/allow-node-client"
	}

	endpoint, err :=
		nodeControlEndpoint(
			node,
			action,
			name,
		)

	if err != nil {
		return err
	}

	request, err := http.NewRequest(
		http.MethodGet,
		endpoint,
		nil,
	)

	if err != nil {
		return err
	}

	if node.Token != "" {
		request.Header.Set(
			"Authorization",
			"Bearer "+node.Token,
		)
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	response, err := client.Do(
		request,
	)

	if err != nil {
		return fmt.Errorf(
			"node %s ACL request failed: %w",
			node.ID,
			err,
		)
	}

	defer response.Body.Close()

	body, _ := io.ReadAll(
		io.LimitReader(
			response.Body,
			8192,
		),
	)

	if response.StatusCode < 200 ||
		response.StatusCode >= 300 {

		return fmt.Errorf(
			"node %s ACL HTTP %d: %s",
			node.ID,
			response.StatusCode,
			strings.TrimSpace(
				string(body),
			),
		)
	}

	return nil
}

func syncClientNodeACL(
	name string,
	selectedNodes []string,
) error {
	selected := map[string]bool{}

	for _, rawID := range selectedNodes {
		id := normalizeNodeID(
			rawID,
		)

		if id != "" {
			selected[id] = true
		}
	}

	nf, err := loadNodesFile()

	if err != nil {
		return fmt.Errorf(
			"load nodes: %w",
			err,
		)
	}

	failures := []string{}

	localSeen := false

	for _, node := range nf.Nodes {
		id := normalizeNodeID(
			node.ID,
		)

		if id == "" {
			continue
		}

		allowed := selected[id]

		isLocal :=
			node.IsLocal ||
				node.AgentURL == "local" ||
				id == "local"

		if isLocal {
			localSeen = true

			if err := setLocalNodeAccess(
				name,
				allowed,
			); err != nil {

				failures = append(
					failures,
					fmt.Sprintf(
						"local: %v",
						err,
					),
				)
			}

			continue
		}

		if err := setRemoteNodeAccess(
			node,
			name,
			allowed,
		); err != nil {

			failures = append(
				failures,
				fmt.Sprintf(
					"%s: %v",
					id,
					err,
				),
			)
		}
	}

	if !localSeen {
		if err := setLocalNodeAccess(
			name,
			selected["local"],
		); err != nil {

			failures = append(
				failures,
				fmt.Sprintf(
					"local: %v",
					err,
				),
			)
		}
	}

	if len(failures) != 0 {
		return fmt.Errorf(
			"%s",
			strings.Join(
				failures,
				"; ",
			),
		)
	}

	return nil
}
