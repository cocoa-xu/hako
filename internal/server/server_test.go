package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cocoa-xu/hako/internal/agent"
	"github.com/cocoa-xu/hako/internal/identity"
	"github.com/cocoa-xu/hako/internal/report"
)

func setup(t *testing.T) (*Server, agent.Config) {
	t.Helper()
	directory := t.TempDir()
	s, err := New(directory, testDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	config := agent.Config{DeviceID: identity.New(), Token: randomSecret(32), Server: "http://127.0.0.1:8787"}
	key, _ := issueKey(t, s)
	response := requestAPI(t, s, "POST", "/api/enroll", key, map[string]string{"device_id": config.DeviceID, "token_hash": hashSecret(config.Token)})
	if response.Code != http.StatusNoContent {
		t.Fatal(response.Code)
	}
	return s, config
}

func post(t *testing.T, s *Server, config agent.Config, snapshot report.Snapshot) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/reports", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+config.Token)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, req)
	return response
}

func TestInventorySurvivesHeartbeatsAndReceiverRestart(t *testing.T) {
	s, config := setup(t)
	hardware := report.OK(report.Hardware{ModelName: "Test Mac"})
	snapshot := report.Snapshot{SchemaVersion: report.SchemaVersion, DeviceID: config.DeviceID, ReportedAt: time.Now().UTC(), Hardware: &hardware}
	if response := post(t, s, config, snapshot); response.Code != http.StatusNoContent {
		t.Fatal(response.Code, response.Body.String())
	}
	snapshot.Hardware = nil
	snapshot.System = report.OK(report.System{Hostname: "renamed-mac"})
	if response := post(t, s, config, snapshot); response.Code != http.StatusNoContent {
		t.Fatal(response.Code)
	}
	restarted := restartServer(t, s)
	machine := restarted.machines[config.DeviceID]
	if machine.Snapshot.Hardware.Data.ModelName != "Test Mac" || !machine.Snapshot.Hardware.ObservedAt.Equal(hardware.ObservedAt) {
		t.Fatal("startup inventory changed")
	}
	if machine.Snapshot.System.Data.Hostname != "renamed-mac" {
		t.Fatal("dynamic update was lost")
	}
	if _, err := os.Stat(filepath.Join(s.directory, "hako.db")); !os.IsNotExist(err) {
		t.Fatal("receiver created a SQLite database")
	}
}

func TestReceiverRequiresInventoryAndCredential(t *testing.T) {
	s, config := setup(t)
	snapshot := report.Snapshot{SchemaVersion: report.SchemaVersion, DeviceID: config.DeviceID, ReportedAt: time.Now().UTC()}
	if response := post(t, s, config, snapshot); response.Code != http.StatusConflict {
		t.Fatal(response.Code)
	}
	bad := config
	bad.Token = strings.Repeat("x", 64)
	if response := post(t, s, bad, snapshot); response.Code != http.StatusUnauthorized {
		t.Fatal(response.Code)
	}
	if len(s.machines) != 0 {
		t.Fatal("rejected report was stored")
	}
	revoke := httptest.NewRequest(http.MethodDelete, "http://127.0.0.1:8787/api/machines/"+config.DeviceID, nil)
	revoke.Header.Set("Authorization", "Bearer "+s.adminKey)
	revoked := httptest.NewRecorder()
	s.Handler().ServeHTTP(revoked, revoke)
	if revoked.Code != http.StatusNoContent {
		t.Fatal("revoke failed", revoked.Code)
	}
	if response := post(t, s, config, snapshot); response.Code != http.StatusUnauthorized {
		t.Fatal("revoked credential accepted")
	}
}

func TestRejectsRebindingAndCrossOriginRequests(t *testing.T) {
	s, _ := setup(t)
	for _, test := range []struct{ host, origin string }{
		{"attacker.example", ""}, {"192.0.2.10:8787", ""}, {"127.0.0.1:8787", "https://attacker.example"}, {"127.0.0.1:8787", "http://127.0.0.1:9999"},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/machines", nil)
		req.Host = test.host
		req.Header.Set("Origin", test.origin)
		req.Header.Set("Authorization", "Bearer "+s.adminKey)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		if response.Code != http.StatusForbidden {
			t.Fatalf("host %s origin %s: %d", test.host, test.origin, response.Code)
		}
	}
}

func TestExplicitLANHostRetainsOriginChecks(t *testing.T) {
	s, _ := setup(t)
	handler := s.Handler("192.0.2.10")
	for _, test := range []struct {
		host, origin string
		want         int
	}{
		{"192.0.2.10:8787", "", http.StatusOK},
		{"192.0.2.10:8787", "http://192.0.2.10:8787", http.StatusOK},
		{"127.0.0.1:8787", "", http.StatusOK},
		{"localhost:8787", "http://localhost:8787", http.StatusOK},
		{"192.0.2.11:8787", "", http.StatusForbidden},
		{"attacker.example:8787", "", http.StatusForbidden},
		{"192.0.2.10:8787", "http://192.0.2.11:8787", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://"+test.host+"/api/machines", nil)
		req.Header.Set("Origin", test.origin)
		req.Header.Set("Authorization", "Bearer "+s.adminKey)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != test.want {
			t.Fatalf("host %s origin %s: got %d, want %d", test.host, test.origin, response.Code, test.want)
		}
	}
}

func TestReportsRejectTrailingJSONAndOversizedBodies(t *testing.T) {
	s, _ := setup(t)
	for _, body := range []string{`{} {}`, strings.Repeat(" ", 1<<20) + `{}`} {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/reports", strings.NewReader(body))
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		if response.Code != http.StatusBadRequest {
			t.Fatal(response.Code)
		}
	}
}

func TestDashboardUsesEmbeddedAssets(t *testing.T) {
	s, _ := setup(t)
	for _, path := range []string{"/", "/app.js", "/style.css", "/icon.svg", "/machine.svg"} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787"+path, nil)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, req)
		if response.Code != http.StatusOK || response.Body.Len() == 0 {
			t.Fatalf("asset %s: %d", path, response.Code)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing cache policy")
		}
	}
}
