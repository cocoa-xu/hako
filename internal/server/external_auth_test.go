package server

import (
	"bufio"
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

func TestExternalAdminPreservesWorkerAuthentication(t *testing.T) {
	directory, database := t.TempDir(), testDatabase(t)
	s, err := NewWithConfig(directory, database, Config{AdminAuth: "external"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := os.Stat(filepath.Join(directory, "admin.key")); !os.IsNotExist(err) {
		t.Fatal("external authentication created an administrator key")
	}
	for _, path := range []string{"/api/machines", "/api/enrollments", "/api/session"} {
		response := requestAPI(t, s, "GET", path, "", nil)
		if response.Code != 200 && response.Code != 204 {
			t.Fatal(path, response.Code)
		}
		if path == "/api/session" && response.Header().Get("X-Hako-Admin-Auth") != "external" {
			t.Fatal("dashboard cannot discover external authentication")
		}
		if len(response.Result().Cookies()) != 0 {
			t.Fatal("external authentication created a session cookie")
		}
	}
	for _, path := range []string{"/api/session", "/api/logout"} {
		request := httptest.NewRequest("POST", "http://127.0.0.1:8787"+path, nil)
		request.Header.Set("Origin", "http://127.0.0.1:8787")
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound || len(s.registry.Sessions) != 0 {
			t.Fatal("external mode accepted local session management")
		}
	}
	request := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/enrollments", strings.NewReader(`{"ttl_seconds":900}`))
	request.Header.Set("Origin", "http://127.0.0.1:8787")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatal(response.Code, response.Body.String())
	}
	var invitation struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &invitation); err != nil {
		t.Fatal(err)
	}
	worker := agent.Config{DeviceID: identity.New(), Token: randomSecret(32)}
	claim := map[string]string{"device_id": worker.DeviceID, "token_hash": hashSecret(worker.Token)}
	if result := requestAPI(t, s, "POST", "/api/enroll", "", claim); result.Code != http.StatusUnauthorized {
		t.Fatal("external mode allowed enrollment without a key")
	}
	if result := requestAPI(t, s, "POST", "/api/enroll", invitation.Key, claim); result.Code != http.StatusNoContent {
		t.Fatal(result.Code)
	}
	hardware := report.OK(report.Hardware{ModelName: "Test Mac"})
	snapshot := report.Snapshot{SchemaVersion: report.SchemaVersion, DeviceID: worker.DeviceID, ReportedAt: time.Now().UTC(), Hardware: &hardware}
	if result := requestAPI(t, s, "POST", "/api/reports", "", snapshot); result.Code != http.StatusUnauthorized {
		t.Fatal("external mode allowed a report without a worker token")
	}
	if result := post(t, s, worker, snapshot); result.Code != http.StatusNoContent {
		t.Fatal(result.Code)
	}
	receiver := httptest.NewServer(s.Handler())
	client := &http.Client{Timeout: 3 * time.Second}
	stream, err := client.Get(receiver.URL + "/api/events")
	if err != nil {
		receiver.Close()
		t.Fatal(err)
	}
	if stream.StatusCode != http.StatusOK {
		stream.Body.Close()
		receiver.Close()
		t.Fatal("external event stream requires a Hako session")
	}
	value := readFleet(t, bufio.NewScanner(stream.Body))
	stream.Body.Close()
	receiver.Close()
	if len(value.Machines) != 1 {
		t.Fatal("external event stream omitted the machine")
	}
	request = httptest.NewRequest("DELETE", "http://127.0.0.1:8787/api/machines/"+worker.DeviceID, nil)
	request.Header.Set("Origin", "http://127.0.0.1:8787")
	response = httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || post(t, s, worker, snapshot).Code != http.StatusUnauthorized {
		t.Fatal("external mode lost worker revocation")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	builtin, err := New(directory, database)
	if err != nil {
		t.Fatal(err)
	}
	defer builtin.Close()
	if result := requestAPI(t, builtin, "GET", "/api/machines", "", nil); result.Code != http.StatusUnauthorized {
		t.Fatal("external mode persisted into default configuration")
	}
}

func TestExternalAdminKeepsOriginChecks(t *testing.T) {
	s, err := NewWithConfig(t.TempDir(), testDatabase(t), Config{AdminAuth: "external", PublicURL: "https://hako.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, test := range []struct {
		origin, marker, bearer string
		want                   int
	}{
		{"", "", "", 403},
		{"", "", "Bearer forged", 403},
		{"https://attacker.example", "", "", 403},
		{"https://attacker.example", "cli", "", 403},
		{"http://hako.example.com", "", "", 403},
		{"https://hako.example.com", "", "", 201},
		{"", "cli", "", 201},
	} {
		request := httptest.NewRequest("POST", "http://hako.example.com/api/enrollments", strings.NewReader(`{"ttl_seconds":900}`))
		request.Header.Set("Origin", test.origin)
		request.Header.Set("X-Hako-Request", test.marker)
		request.Header.Set("Authorization", test.bearer)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if response.Code != test.want {
			t.Fatalf("origin %q marker %q: got %d, want %d", test.origin, test.marker, response.Code, test.want)
		}
	}
}

func TestBuiltinAdminDoesNotTrustProxyHeaders(t *testing.T) {
	s, _ := setup(t)
	request := httptest.NewRequest("GET", "http://127.0.0.1:8787/api/machines", nil)
	request.Header.Set("CF-Access-Jwt-Assertion", "unverified")
	request.Header.Set("CF-Access-Authenticated-User-Email", "admin@example.com")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatal("proxy headers bypassed builtin authentication")
	}
	if _, err := NewWithConfig(t.TempDir(), "", Config{AdminAuth: "typo"}); err == nil {
		t.Fatal("unknown authentication mode accepted")
	}
}
