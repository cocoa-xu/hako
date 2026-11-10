package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hako/internal/agent"
	"hako/internal/identity"
	"hako/internal/report"
)

func requestAPI(t *testing.T, s *Server, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, bytes.NewReader(data))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, req)
	return response
}

func issueKey(t *testing.T, s *Server) (string, enrollment) {
	t.Helper()
	response := requestAPI(t, s, "POST", "/api/enrollments", s.adminKey, map[string]any{"label": "Build worker", "ttl_seconds": 900})
	if response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	var value struct {
		enrollment
		Key string `json:"key"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value.Key, value.enrollment
}

func TestEnrollmentIsSingleUseDurableAndRevocable(t *testing.T) {
	s, _ := setup(t)
	key, item := issueKey(t, s)
	config := agent.Config{DeviceID: identity.New(), Token: randomSecret(32), Server: "http://127.0.0.1:8787"}
	claim := map[string]string{"device_id": config.DeviceID, "token_hash": hashSecret(config.Token)}
	for i := 0; i < 2; i++ {
		if response := requestAPI(t, s, "POST", "/api/enroll", key, claim); response.Code != 204 {
			t.Fatal(response.Code)
		}
	}
	other := map[string]string{"device_id": identity.New(), "token_hash": hashSecret(randomSecret(32))}
	if response := requestAPI(t, s, "POST", "/api/enroll", key, other); response.Code != 409 {
		t.Fatal("reused key accepted")
	}
	data, err := os.ReadFile(filepath.Join(s.directory, "hako.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(key)) || bytes.Contains(data, []byte(config.Token)) {
		t.Fatal("plaintext device or enrollment credential persisted")
	}
	restarted, err := New(s.directory)
	if err != nil {
		t.Fatal(err)
	}
	if response := requestAPI(t, restarted, "POST", "/api/enroll", key, other); response.Code != 409 {
		t.Fatal("key consumption lost on restart")
	}
	hardware := report.OK(report.Hardware{ModelName: "Test Mac"})
	snapshot := report.Snapshot{SchemaVersion: report.SchemaVersion, DeviceID: config.DeviceID, ReportedAt: time.Now(), Hardware: &hardware}
	if response := post(t, restarted, config, snapshot); response.Code != 204 {
		t.Fatal(response.Code)
	}
	if response := requestAPI(t, restarted, "DELETE", "/api/machines/"+config.DeviceID, restarted.adminKey, nil); response.Code != 204 {
		t.Fatal(response.Code)
	}
	if response := post(t, restarted, config, snapshot); response.Code != 401 {
		t.Fatal("revoked worker accepted")
	}
	if response := requestAPI(t, restarted, "POST", "/api/enroll", key, claim); response.Code != 409 {
		t.Fatal("used key resurrected revoked worker")
	}
	restarted, err = New(s.directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := restarted.machines[config.DeviceID]; exists {
		t.Fatal("removed machine returned after restart")
	}
	if response := post(t, restarted, config, snapshot); response.Code != 401 {
		t.Fatal("revocation lost on restart")
	}
	if response := requestAPI(t, restarted, "DELETE", "/api/enrollments/"+item.ID, restarted.adminKey, nil); response.Code != 204 {
		t.Fatal(response.Code)
	}
}

func TestEnrollmentExpiryLimitsAndConcurrentClaim(t *testing.T) {
	s, _ := setup(t)
	for _, ttl := range []int{0, 59, 86401} {
		if response := requestAPI(t, s, "POST", "/api/enrollments", s.adminKey, map[string]any{"ttl_seconds": ttl}); response.Code != 400 {
			t.Fatal("invalid lifetime accepted", ttl)
		}
	}
	key, item := issueKey(t, s)
	item.ExpiresAt = time.Now().Add(-time.Second)
	s.registry.Enrollments[hashSecret(key)] = item
	claim := map[string]string{"device_id": identity.New(), "token_hash": hashSecret(randomSecret(32))}
	if response := requestAPI(t, s, "POST", "/api/enroll", key, claim); response.Code != 410 {
		t.Fatal("expired key accepted")
	}
	key, _ = issueKey(t, s)
	var wait sync.WaitGroup
	results := make(chan int, 12)
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response := requestAPI(t, s, "POST", "/api/enroll", key, map[string]string{"device_id": identity.New(), "token_hash": hashSecret(randomSecret(32))})
			results <- response.Code
		}()
	}
	wait.Wait()
	close(results)
	successes := 0
	for status := range results {
		if status == 204 {
			successes++
		} else if status != 409 {
			t.Fatal(status)
		}
	}
	if successes != 1 {
		t.Fatalf("one key registered %d machines", successes)
	}
}

func TestAdministratorBoundarySessionsAndLogout(t *testing.T) {
	s, worker := setup(t)
	for _, path := range []string{"/api/machines", "/api/events", "/api/enrollments", "/api/session"} {
		for _, key := range []string{"", worker.Token} {
			if response := requestAPI(t, s, "GET", path, key, nil); response.Code != 401 {
				t.Fatal("unauthorized read", path)
			}
		}
	}
	for _, path := range []string{"/api/enrollments", "/api/session"} {
		if response := requestAPI(t, s, "POST", path, worker.Token, map[string]int{"ttl_seconds": 900}); response.Code != 401 {
			t.Fatal("worker became administrator")
		}
	}
	login := requestAPI(t, s, "POST", "/api/session", s.adminKey, nil)
	if login.Code != 204 || len(login.Result().Cookies()) != 1 {
		t.Fatal("login failed")
	}
	cookie := login.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 43200 {
		t.Fatal("unsafe cookie")
	}
	request := httptest.NewRequest("GET", "http://127.0.0.1:8787/api/machines", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal("session not accepted")
	}
	logout := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/logout", nil)
	logout.AddCookie(cookie)
	response = httptest.NewRecorder()
	s.Handler().ServeHTTP(response, logout)
	if response.Code != 403 {
		t.Fatal("cookie mutation without Origin accepted")
	}
	logout.Header.Set("Origin", "http://127.0.0.1:8787")
	response = httptest.NewRecorder()
	s.Handler().ServeHTTP(response, logout)
	if response.Code != 204 {
		t.Fatal("logout failed")
	}
	response = httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("logged-out session replayed")
	}
	restarted, err := New(s.directory)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.authorized(request) {
		t.Fatal("logout lost on restart")
	}
}

func TestProxyOriginSecureCookiesAndAdminRotation(t *testing.T) {
	s, _ := setup(t)
	s, err := NewWithOrigin(s.directory, "https://hako.example.com")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "http://hako.example.com/api/session", nil)
	request.Header.Set("Origin", "https://hako.example.com")
	request.Header.Set("Authorization", "Bearer "+s.adminKey)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != 204 || !response.Result().Cookies()[0].Secure {
		t.Fatal("proxy login rejected or cookie insecure")
	}
	cookie := response.Result().Cookies()[0]
	request = httptest.NewRequest("GET", "http://hako.example.com/api/machines", nil)
	request.AddCookie(cookie)
	s.registry.Sessions[hashSecret(cookie.Value)] = time.Now().Add(-time.Second)
	if s.authorized(request) {
		t.Fatal("expired session accepted")
	}
	for _, origin := range []string{"http://hako.example.com", "https://attacker.example"} {
		request.Header.Set("Origin", origin)
		response = httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if response.Code != 403 {
			t.Fatal("cross-origin request accepted", origin)
		}
	}
	if err := os.WriteFile(filepath.Join(s.directory, "admin.key"), []byte(strings.Repeat("a", 64)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(s.directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.registry.Sessions) != 0 {
		t.Fatal("admin key rotation preserved sessions")
	}
	request.Header.Set("Authorization", "Bearer "+s.adminKey)
	if restarted.authorized(request) {
		t.Fatal("old admin key accepted after rotation")
	}
}

func TestEnrollmentNamesAndBearerScheme(t *testing.T) {
	s, _ := setup(t)
	for _, name := range []string{strings.Repeat("箱", 81), "Mac\nName", "Mac\x00Name"} {
		response := requestAPI(t, s, "POST", "/api/enrollments", s.adminKey, map[string]any{"label": name, "ttl_seconds": 900})
		if response.Code != 400 {
			t.Fatal("invalid enrollment name accepted")
		}
	}
	response := requestAPI(t, s, "POST", "/api/enrollments", s.adminKey, map[string]any{"label": strings.Repeat("箱", 80), "ttl_seconds": 900})
	if response.Code != 201 {
		t.Fatal("valid Unicode name rejected")
	}
	key, _ := issueKey(t, s)
	body, _ := json.Marshal(map[string]string{"device_id": identity.New(), "token_hash": hashSecret(randomSecret(32))})
	request := httptest.NewRequest("POST", "http://127.0.0.1:8787/api/enroll", bytes.NewReader(body))
	request.Header.Set("Authorization", key)
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, request)
	if result.Code != 401 {
		t.Fatal("missing Bearer scheme accepted")
	}
}
