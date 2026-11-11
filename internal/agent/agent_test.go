package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cocoa-xu/hako/internal/report"
)

type fakeCollector struct{ inventories int }

func (c *fakeCollector) Inventory(context.Context) report.Section[report.Hardware] {
	c.inventories++
	return report.OK(report.Hardware{ModelName: "Test Mac"})
}

func (c *fakeCollector) Sample(_ context.Context, id string) report.Snapshot {
	return report.Snapshot{SchemaVersion: report.SchemaVersion, DeviceID: id, ReportedAt: time.Now().UTC()}
}

func TestStartupInventoryIsCollectedOnceAndRetried(t *testing.T) {
	var reports []report.Snapshot
	statuses := []int{http.StatusServiceUnavailable, http.StatusNoContent, http.StatusNoContent, http.StatusConflict, http.StatusNoContent}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var snapshot report.Snapshot
		if err := json.NewDecoder(r.Body).Decode(&snapshot); err != nil {
			t.Error(err)
		}
		reports = append(reports, snapshot)
		w.WriteHeader(statuses[len(reports)-1])
	}))
	defer receiver.Close()
	c := &fakeCollector{}
	a, err := New(Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: "0123456789abcdef0123456789abcdef", Server: receiver.URL}, c)
	if err != nil {
		t.Fatal(err)
	}
	for i, status := range statuses {
		err := a.Step(context.Background())
		if (err == nil) != (status == http.StatusNoContent) {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	if c.inventories != 1 {
		t.Fatalf("hardware collected %d times", c.inventories)
	}
	for i, expected := range []bool{true, true, false, false, true} {
		if (reports[i].Hardware != nil) != expected {
			t.Fatalf("report %d hardware inclusion is wrong", i)
		}
	}
	if !reports[0].Hardware.ObservedAt.Equal(reports[4].Hardware.ObservedAt) {
		t.Fatal("retried inventory was not the original observation")
	}
}

func TestRedirectDoesNotForwardCredentials(t *testing.T) {
	called := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer destination.Close()
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer receiver.Close()
	a, _ := New(Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: "0123456789abcdef0123456789abcdef", Server: receiver.URL}, &fakeCollector{})
	if err := a.Step(context.Background()); err == nil {
		t.Fatal("redirect accepted")
	}
	if called {
		t.Fatal("redirect followed")
	}
}

func TestConfigRejectsUnsafeOrigins(t *testing.T) {
	for _, endpoint := range []string{"http://example.com", "http://localhost", "file:///tmp/test", "https://user:password@example.com", "https://example.com/path", "https://example.com?token=x"} {
		config := Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: "0123456789abcdef0123456789abcdef", Server: endpoint}
		if config.Validate() == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
}
