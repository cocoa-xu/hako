package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cocoa-xu/hako/internal/agent"
)

func TestManagementAuthenticationModes(t *testing.T) {
	access := &agent.AccessServiceToken{ClientID: "admin.access", ClientSecret: "admin-test-secret"}
	for _, key := range []string{strings.Repeat("a", 64), ""} {
		t.Run(map[bool]string{true: "external", false: "builtin"}[key == ""], func(t *testing.T) {
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if key == "" {
					if r.Header.Get("Authorization") != "" || r.Header.Get("X-Hako-Request") != "cli" {
						t.Error("external mutation lacks its non-browser request marker")
					}
				} else if r.Header.Get("Authorization") != "Bearer "+key {
					t.Error("administrator bearer key missing")
				}
				if r.Header.Get("CF-Access-Client-Id") != access.ClientID || r.Header.Get("CF-Access-Client-Secret") != access.ClientSecret {
					t.Error("administrator Access credentials missing")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer receiver.Close()
			if _, err := Call(context.Background(), receiver.URL, key, "POST", "/api/enrollments", map[string]int{"ttl_seconds": 900}, Options{CloudflareAccess: access}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestManagementRedirectDoesNotForwardCredentials(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("administrator credentials followed a redirect")
	}))
	defer destination.Close()
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer receiver.Close()
	options := Options{CloudflareAccess: &agent.AccessServiceToken{ClientID: "admin.access", ClientSecret: "admin-test-secret"}}
	if _, err := Call(context.Background(), receiver.URL, strings.Repeat("a", 64), "GET", "/api/machines", nil, options); err == nil {
		t.Fatal("redirect was accepted")
	}
}
