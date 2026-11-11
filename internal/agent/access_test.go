package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccessEnrollmentReportsAndRotation(t *testing.T) {
	access := &AccessServiceToken{ClientID: "test-client.access", ClientSecret: "test-secret"}
	key := strings.Repeat("b", 64)
	var workerToken string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("CF-Access-Client-Id") != access.ClientID || r.Header.Get("CF-Access-Client-Secret") != access.ClientSecret {
			t.Error("missing Access service token")
		}
		want := key
		if r.URL.Path == "/api/reports" {
			want = workerToken
		}
		if r.Header.Get("Authorization") != "Bearer "+want {
			t.Error("Access replaced Hako authentication")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	path := filepath.Join(t.TempDir(), "worker.json")
	if err := JoinWithOptions(context.Background(), receiver.URL, key, path, JoinOptions{CloudflareAccess: access, AllowHTTP: true}); err != nil {
		t.Fatal(err)
	}
	load := func() Config {
		t.Helper()
		data, err := ReadSecret(path)
		if err != nil {
			t.Fatal(err)
		}
		var config Config
		if err := json.Unmarshal([]byte(data), &config); err != nil {
			t.Fatal(err)
		}
		return config
	}
	config := load()
	if !config.AllowHTTP {
		t.Fatal("explicit HTTP transport choice was not saved")
	}
	workerToken = config.Token
	a, err := New(config, &fakeCollector{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []*AccessServiceToken{{ClientID: "replacement.access", ClientSecret: "replacement-secret"}, nil} {
		if err := ConfigureAccess(path, replacement); err != nil {
			t.Fatal(err)
		}
		updated := load()
		if updated.DeviceID != config.DeviceID || updated.Token != config.Token || updated.Pending || updated.Server != config.Server || updated.AllowHTTP != config.AllowHTTP {
			t.Fatal("Access update changed worker identity")
		}
		if replacement == nil && updated.CloudflareAccess != nil || replacement != nil && (updated.CloudflareAccess == nil || *updated.CloudflareAccess != *replacement) {
			t.Fatal("Access update was not persisted")
		}
	}
}

func TestPrivateHTTPRequiresExplicitOptIn(t *testing.T) {
	config := Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: strings.Repeat("a", 64)}
	for _, origin := range []string{"http://100.64.0.10:8787", "http://192.0.2.10:8787"} {
		config.Server = origin
		config.AllowHTTP = false
		if config.Validate() == nil {
			t.Fatal("HTTP was allowed implicitly")
		}
		config.AllowHTTP = true
		if err := config.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, origin := range []string{"ftp://192.0.2.10", "http://user:password@192.0.2.10", "http://192.0.2.10/path"} {
		config.Server = origin
		if config.Validate() == nil {
			t.Fatal("HTTP opt-in disabled origin validation")
		}
	}
}

func TestAccessCredentialValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.json")
	for _, data := range []string{`{}`, `null`, `{"client_id":"only-id"}`, `{"client_secret":"only-secret"}`, `{"client_id":"id","client_secret":"line\nbreak"}`, `{"client_id":" id","client_secret":"secret"}`, `not-json`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadAccessServiceToken(path); err == nil {
			t.Fatal("invalid Access credentials accepted")
		}
	}
	if err := os.WriteFile(path, []byte(`{"client_id":"test.access","client_secret":"test-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAccessServiceToken(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAccessServiceToken(path); err == nil {
		t.Fatal("shared Access credential file accepted")
	}
}

func TestAccessEnrollmentDoesNotFollowRedirects(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("redirect leaked enrollment or Access credentials")
	}))
	defer destination.Close()
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer receiver.Close()
	access := &AccessServiceToken{ClientID: "test.access", ClientSecret: "test-secret"}
	if err := JoinWithOptions(context.Background(), receiver.URL, strings.Repeat("b", 64), filepath.Join(t.TempDir(), "worker.json"), JoinOptions{CloudflareAccess: access}); err == nil {
		t.Fatal("Access login redirect reported success")
	}
}
