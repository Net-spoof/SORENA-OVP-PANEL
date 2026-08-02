package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConnectionLimitKeepsOldestSessions(
	t *testing.T,
) {
	now :=
		time.Date(
			2026,
			8,
			2,
			2,
			0,
			0,
			0,
			time.UTC,
		)

	sessions :=
		[]localOpenVPNSession{
			{
				ClientName:     "reza",
				RealAddress:    "1.1.1.1:1001",
				ClientID:       "primary-old",
				ConnectedAt:    now.Add(-3 * time.Minute),
				ManagementPort: 7505,
			},
			{
				ClientName:     "reza",
				RealAddress:    "1.1.1.1:1002",
				ClientID:       "germany-middle",
				ConnectedAt:    now.Add(-2 * time.Minute),
				ManagementPort: 7511,
			},
			{
				ClientName:     "reza",
				RealAddress:    "1.1.1.1:1003",
				ClientID:       "finland-new",
				ConnectedAt:    now.Add(-1 * time.Minute),
				ManagementPort: 7512,
			},
		}

	excess :=
		connectionLimitExcessSessions(
			sessions,
			2,
		)

	if len(excess) != 1 {
		t.Fatalf(
			"expected one excess session, got %d",
			len(excess),
		)
	}

	if excess[0].ClientID !=
		"finland-new" {

		t.Fatalf(
			"expected newest session to be killed, got %+v",
			excess[0],
		)
	}

	if sessions[0].ClientID !=
		"primary-old" {

		t.Fatal(
			"input slice was modified",
		)
	}
}

func TestPersistCreatedClientMetadataAtomic(
	t *testing.T,
) {
	directory := t.TempDir()

	path := filepath.Join(
		directory,
		"alice.json",
	)

	original :=
		[]byte(
			`{"name":"alice","active":true}`,
		)

	if err := os.WriteFile(
		path,
		original,
		0644,
	); err != nil {
		t.Fatal(err)
	}

	if err := persistCreatedClientMetadata(
		path,
		30,
		123456,
		2,
		"alice@example.com",
		[]string{
			"local",
			"germany-node",
			"germany-node",
		},
		[]string{
			"germany",
			"finland",
		},
	); err != nil {
		t.Fatal(err)
	}

	payload, err :=
		os.ReadFile(path)

	if err != nil {
		t.Fatal(err)
	}

	var data map[string]interface{}

	if err := json.Unmarshal(
		payload,
		&data,
	); err != nil {
		t.Fatal(err)
	}

	if int(
		data["connection_limit"].(float64),
	) != 2 {
		t.Fatalf(
			"connection limit not persisted: %+v",
			data,
		)
	}

	nodes :=
		data["nodes"].([]interface{})

	if len(nodes) != 2 ||
		nodes[0] != "local" ||
		nodes[1] != "germany-node" {

		t.Fatalf(
			"unexpected nodes: %+v",
			nodes,
		)
	}

	locations :=
		data["openvpn_port_locations"].([]interface{})

	if len(locations) != 2 ||
		locations[0] != "germany" ||
		locations[1] != "finland" {

		t.Fatalf(
			"unexpected port locations: %+v",
			locations,
		)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0644 {
		t.Fatalf(
			"unexpected metadata mode: %o",
			info.Mode().Perm(),
		)
	}
}

func TestPersistCreatedClientMetadataRejectsMalformedJSON(
	t *testing.T,
) {
	path :=
		filepath.Join(
			t.TempDir(),
			"broken.json",
		)

	original :=
		[]byte(`{"name":`)

	if err := os.WriteFile(
		path,
		original,
		0644,
	); err != nil {
		t.Fatal(err)
	}

	err :=
		persistCreatedClientMetadata(
			path,
			0,
			0,
			1,
			"",
			nil,
			[]string{"germany"},
		)

	if err == nil {
		t.Fatal(
			"malformed metadata must fail",
		)
	}

	after, readErr :=
		os.ReadFile(path)

	if readErr != nil {
		t.Fatal(readErr)
	}

	if string(after) != string(original) {
		t.Fatal(
			"malformed source was modified",
		)
	}
}
