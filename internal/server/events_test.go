package server

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cocoa-xu/hako/internal/report"
)

func readFleet(t *testing.T, scanner *bufio.Scanner) fleet {
	t.Helper()
	for scanner.Scan() {
		if payload, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
			var value fleet
			if err := json.Unmarshal([]byte(payload), &value); err != nil {
				t.Fatal(err)
			}
			return value
		}
	}
	t.Fatalf("event stream ended: %v", scanner.Err())
	return fleet{}
}

func TestEventStreamDeliversReportsAndReconnectSnapshot(t *testing.T) {
	s, config := setup(t)
	receiver := httptest.NewServer(s.Handler())
	defer receiver.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	openStream := func() (*http.Response, error) {
		req, _ := http.NewRequest(http.MethodGet, receiver.URL+"/api/events", nil)
		req.Header.Set("Authorization", "Bearer "+s.adminKey)
		return client.Do(req)
	}
	response, err := openStream()
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("not an event stream")
	}
	scanner := bufio.NewScanner(response.Body)
	if len(readFleet(t, scanner).Machines) != 0 {
		t.Fatal("initial snapshot incorrect")
	}
	hardware := report.OK(report.Hardware{ModelName: "Test Mac"})
	snapshot := report.Snapshot{SchemaVersion: report.SchemaVersion, DeviceID: config.DeviceID, ReportedAt: time.Now().UTC(), Hardware: &hardware, Traffic: report.OK(report.Traffic{ReceivedBytes: 12345})}
	if result := post(t, s, config, snapshot); result.Code != http.StatusNoContent {
		t.Fatal(result.Code)
	}
	if next := readFleet(t, scanner); len(next.Machines) != 1 || next.Machines[0].Snapshot.Traffic.Data.ReceivedBytes != 12345 {
		t.Fatal("new report not pushed")
	}
	response.Body.Close()
	response, err = openStream()
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if len(readFleet(t, bufio.NewScanner(response.Body)).Machines) != 1 {
		t.Fatal("reconnect did not receive current state")
	}
	response.Body.Close()
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		remaining := len(s.subscribers)
		s.mu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnected stream leaked a subscriber")
		}
		time.Sleep(time.Millisecond)
	}
}
