package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testOpenVPNClient(
	source string,
	name string,
	address string,
	connected time.Time,
	received int64,
	sent int64,
) OpenVPNClient {
	client := OpenVPNClient{
		Source:         source,
		CommonName:     name,
		RealAddress:    address,
		ConnectedSince: connected,
		BytesReceived:  received,
		BytesSent:      sent,
	}

	client.SessionKey =
		openVPNSessionKey(client)

	return client
}

func TestOpenVPNMultiPortTrafficNoDoubleCount(
	t *testing.T,
) {
	database :=
		&TrafficDatabase{
			DataDir: t.TempDir(),
		}

	now :=
		time.Date(
			2026,
			8,
			2,
			0,
			0,
			0,
			0,
			time.UTC,
		)

	connectedA :=
		now.Add(
			-10 * time.Minute,
		)

	connectedB :=
		now.Add(
			-5 * time.Minute,
		)

	first := []OpenVPNClient{
		testOpenVPNClient(
			"/status-443.log",
			"alice",
			"1.1.1.1:1000",
			connectedA,
			100,
			50,
		),
		testOpenVPNClient(
			"/status-8443.log",
			"alice",
			"1.1.1.1:2000",
			connectedB,
			40,
			10,
		),
	}

	_, totals, err :=
		database.
			ApplyOpenVPNMultiPortSnapshots(
				first,
				now,
			)

	if err != nil {
		t.Fatal(err)
	}

	if totals["alice"].Received != 0 ||
		totals["alice"].Sent != 0 {

		t.Fatalf(
			"first snapshot must only establish baseline: %+v",
			totals["alice"],
		)
	}

	second := []OpenVPNClient{
		testOpenVPNClient(
			"/status-443.log",
			"alice",
			"1.1.1.1:1000",
			connectedA,
			130,
			70,
		),
		testOpenVPNClient(
			"/status-8443.log",
			"alice",
			"1.1.1.1:2000",
			connectedB,
			55,
			15,
		),
	}

	_, totals, err =
		database.
			ApplyOpenVPNMultiPortSnapshots(
				second,
				now.Add(
					20*time.Second,
				),
			)

	if err != nil {
		t.Fatal(err)
	}

	if totals["alice"].Received != 45 ||
		totals["alice"].Sent != 25 {

		t.Fatalf(
			"unexpected total after two sessions: %+v",
			totals["alice"],
		)
	}

	third := []OpenVPNClient{
		testOpenVPNClient(
			"/status-8443.log",
			"alice",
			"1.1.1.1:2000",
			connectedB,
			65,
			20,
		),
	}

	_, totals, err =
		database.
			ApplyOpenVPNMultiPortSnapshots(
				third,
				now.Add(
					40*time.Second,
				),
			)

	if err != nil {
		t.Fatal(err)
	}

	if totals["alice"].Received != 55 ||
		totals["alice"].Sent != 30 {

		t.Fatalf(
			"disconnected session was counted again: %+v",
			totals["alice"],
		)
	}

	connectedC :=
		now.Add(
			50 * time.Second,
		)

	fourth := append(
		third,
		testOpenVPNClient(
			"/status-9443.log",
			"alice",
			"1.1.1.1:3000",
			connectedC,
			20,
			10,
		),
	)

	_, totals, err =
		database.
			ApplyOpenVPNMultiPortSnapshots(
				fourth,
				now.Add(
					60*time.Second,
				),
			)

	if err != nil {
		t.Fatal(err)
	}

	if totals["alice"].Received != 75 ||
		totals["alice"].Sent != 40 {

		t.Fatalf(
			"third-port session was not counted correctly: %+v",
			totals["alice"],
		)
	}
}

func TestOpenVPNMultiPortTrafficExternalReset(
	t *testing.T,
) {
	database :=
		&TrafficDatabase{
			DataDir: t.TempDir(),
		}

	now :=
		time.Date(
			2026,
			8,
			2,
			1,
			0,
			0,
			0,
			time.UTC,
		)

	connected :=
		now.Add(
			-5 * time.Minute,
		)

	first :=
		[]OpenVPNClient{
			testOpenVPNClient(
				"/status-443.log",
				"reza",
				"2.2.2.2:1000",
				connected,
				100,
				50,
			),
		}

	if _, _, err :=
		database.
			ApplyOpenVPNMultiPortSnapshots(
				first,
				now,
			); err != nil {

		t.Fatal(err)
	}

	second :=
		[]OpenVPNClient{
			testOpenVPNClient(
				"/status-443.log",
				"reza",
				"2.2.2.2:1000",
				connected,
				130,
				70,
			),
		}

	if _, _, err :=
		database.
			ApplyOpenVPNMultiPortSnapshots(
				second,
				now.Add(
					20*time.Second,
				),
			); err != nil {

		t.Fatal(err)
	}

	resetTotals :=
		[]ClientTotalTraffic{
			{
				ClientName: "reza",

				TotalReceived: 0,

				TotalSent: 0,

				LastSessionReceived: 0,

				LastSessionSent: 0,

				LastUpdate: now.Add(
					30 * time.Second,
				),
			},
		}

	payload, err :=
		json.MarshalIndent(
			resetTotals,
			"",
			"  ",
		)

	if err != nil {
		t.Fatal(err)
	}

	cumulativePath :=
		filepath.Join(
			database.DataDir,
			"cumulative_totals.json",
		)

	if err := os.WriteFile(
		cumulativePath,
		payload,
		0644,
	); err != nil {
		t.Fatal(err)
	}

	future :=
		now.Add(
			2 * time.Hour,
		)

	if err := os.Chtimes(
		cumulativePath,
		future,
		future,
	); err != nil {
		t.Fatal(err)
	}

	afterReset :=
		[]OpenVPNClient{
			testOpenVPNClient(
				"/status-443.log",
				"reza",
				"2.2.2.2:1000",
				connected,
				150,
				80,
			),
		}

	_, totals, err :=
		database.
			ApplyOpenVPNMultiPortSnapshots(
				afterReset,
				now.Add(
					40*time.Second,
				),
			)

	if err != nil {
		t.Fatal(err)
	}

	if totals["reza"].Received != 0 ||
		totals["reza"].Sent != 0 {

		t.Fatalf(
			"traffic reset was resurrected: %+v",
			totals["reza"],
		)
	}

	next :=
		[]OpenVPNClient{
			testOpenVPNClient(
				"/status-443.log",
				"reza",
				"2.2.2.2:1000",
				connected,
				160,
				85,
			),
		}

	_, totals, err =
		database.
			ApplyOpenVPNMultiPortSnapshots(
				next,
				now.Add(
					60*time.Second,
				),
			)

	if err != nil {
		t.Fatal(err)
	}

	if totals["reza"].Received != 10 ||
		totals["reza"].Sent != 5 {

		t.Fatalf(
			"post-reset traffic is incorrect: %+v",
			totals["reza"],
		)
	}
}
