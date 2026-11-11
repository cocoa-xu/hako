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

func TestJoinRetainsPendingIdentityAfterLostAcknowledgement(t *testing.T) {
	var claims []map[string]string
	key := strings.Repeat("b", 64)
	access := &AccessServiceToken{ClientID: "test-client.access", ClientSecret: "test-secret"}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/enroll" || r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("incorrect registration request")
		}
		if r.Header.Get("CF-Access-Client-Id") != access.ClientID || r.Header.Get("CF-Access-Client-Secret") != access.ClientSecret {
			t.Error("registration lost Access credentials")
		}
		var claim map[string]string
		if err := json.NewDecoder(r.Body).Decode(&claim); err != nil {
			t.Error(err)
		}
		claims = append(claims, claim)
		if len(claims) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer receiver.Close()
	path := filepath.Join(t.TempDir(), "worker.json")
	if err := JoinWithOptions(context.Background(), receiver.URL, key, path, JoinOptions{CloudflareAccess: access}); err == nil {
		t.Fatal("failed registration reported success")
	}
	text, err := ReadSecret(path)
	if err != nil {
		t.Fatal(err)
	}
	var pending Config
	if err := json.Unmarshal([]byte(text), &pending); err != nil {
		t.Fatal(err)
	}
	if !pending.Pending {
		t.Fatal("pending identity not saved")
	}
	if _, err := New(pending, &fakeCollector{}); err == nil {
		t.Fatal("pending identity allowed to report")
	}
	if err := Join(context.Background(), receiver.URL, key, path); err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 || claims[0]["device_id"] != claims[1]["device_id"] || claims[0]["token_hash"] != claims[1]["token_hash"] {
		t.Fatal("retry changed installation identity")
	}
	if claims[0]["token_hash"] == pending.Token {
		t.Fatal("raw device token sent during enrollment")
	}
	text, _ = ReadSecret(path)
	var config Config
	if err := json.Unmarshal([]byte(text), &config); err != nil {
		t.Fatal(err)
	}
	if config.Pending || config.Token != pending.Token {
		t.Fatal("registration did not preserve the credential")
	}
	if strings.Contains(text, key) {
		t.Fatal("enrollment key persisted on worker")
	}
	if err := Join(context.Background(), receiver.URL, key, path); err == nil {
		t.Fatal("existing enrolled identity overwritten")
	}
}

func TestJoinRejectsSharedCredentialsAndUnsafeServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.key")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecret(path); err == nil {
		t.Fatal("shared credential accepted")
	}
	for _, server := range []string{"http://hako.example.com", "https://example.com/path", "https://user:password@example.com"} {
		if err := Join(context.Background(), server, strings.Repeat("b", 64), filepath.Join(t.TempDir(), "worker.json")); err == nil {
			t.Fatal("unsafe server accepted")
		}
	}
}
