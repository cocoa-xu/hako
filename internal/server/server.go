package server

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"hako/internal/agent"
	"hako/internal/identity"
	"hako/internal/report"
)

//go:embed web/*
var assets embed.FS

type Machine struct {
	DisplayName string          `json:"display_name,omitempty"`
	Snapshot    report.Snapshot `json:"snapshot"`
	ReceivedAt  time.Time       `json:"received_at"`
}

type Server struct {
	db          *sql.Conn
	pool        *sql.DB
	databaseURL string
	directory   string
	mu          sync.Mutex
	machines    map[string]Machine
	subscribers map[chan struct{}]struct{}
	registry    registry
	adminKey    string
	publicURL   *url.URL
}

func validID(id string) bool { return identity.Valid(id) }

func Prepare(directory string) error {
	for _, path := range []string{directory, filepath.Join(directory, "agents"), filepath.Join(directory, "machines")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
	}
	return nil
}

func New(directory, databaseURL string) (*Server, error) {
	return NewWithOrigin(directory, databaseURL, "")
}

// NewWithOrigin configures the external HTTPS origin used by a reverse proxy.
func NewWithOrigin(directory, databaseURL, origin string) (*Server, error) {
	if err := Prepare(directory); err != nil {
		return nil, err
	}
	s := &Server{directory: directory, databaseURL: databaseURL, machines: make(map[string]Machine), subscribers: make(map[chan struct{}]struct{})}
	if origin != "" {
		config := agent.Config{Server: origin, DeviceID: strings.Repeat("0", 32), Token: strings.Repeat("0", 64)}
		if err := config.Validate(); err != nil {
			return nil, fmt.Errorf("public URL: %w", err)
		}
		s.publicURL, _ = url.Parse(strings.TrimRight(origin, "/"))
	}
	if err := s.openDatabase(databaseURL); err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = s.Close()
		}
	}()
	if err := s.loadRegistry(); err != nil {
		return nil, err
	}
	if err := s.loadAdminKey(); err != nil {
		return nil, err
	}
	if err := s.loadMachines(); err != nil {
		return nil, err
	}
	success = true
	return s, nil
}

func (s *Server) Handler(additionalHosts ...string) http.Handler {
	allowedHosts := make(map[string]bool, len(additionalHosts))
	for _, host := range additionalHosts {
		if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
			allowedHosts[ip.String()] = true
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/machines", s.requireAdmin(s.list))
	mux.HandleFunc("GET /api/events", s.requireAdmin(s.events))
	mux.HandleFunc("GET /api/session", s.requireAdmin(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	mux.HandleFunc("POST /api/session", s.login)
	mux.HandleFunc("POST /api/logout", s.requireAdmin(s.logout))
	mux.HandleFunc("POST /api/enroll", s.register)
	mux.HandleFunc("POST /api/enrollments", s.requireAdmin(s.createEnrollment))
	mux.HandleFunc("GET /api/enrollments", s.requireAdmin(s.listEnrollments))
	mux.HandleFunc("DELETE /api/enrollments/{id}", s.requireAdmin(s.revokeEnrollment))
	mux.HandleFunc("DELETE /api/machines/{id}", s.requireAdmin(s.revokeMachine))
	mux.HandleFunc("PATCH /api/machines/{id}", s.requireAdmin(s.renameMachine))
	mux.HandleFunc("POST /api/reports", s.receive)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.db.PingContext(ctx); err != nil {
			http.Error(w, "Database unavailable.", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	web, _ := fs.Sub(assets, "web")
	mux.Handle("GET /", http.FileServer(http.FS(web)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		w.Header().Set("Cache-Control", "no-store")
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(host)
		publicHost := s.publicURL != nil && r.Host == s.publicURL.Host
		if !publicHost && host != "localhost" && (ip == nil || (!ip.IsLoopback() && !allowedHosts[ip.String()])) {
			http.Error(w, "This host is not configured for the receiver.", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			scheme := "http"
			if s.secureRequest(r) {
				scheme = "https"
			}
			if err != nil || u.Host != r.Host || u.Scheme != scheme || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
				http.Error(w, "Cross-origin requests are not allowed.", http.StatusForbidden)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

type fleet struct {
	Machines   []Machine `json:"machines"`
	ServerTime time.Time `json:"server_time"`
}

func (s *Server) fleet() fleet {
	s.mu.Lock()
	machines := make([]Machine, 0, len(s.machines))
	for id, machine := range s.machines {
		machine.DisplayName = s.registry.Devices[id].DisplayName
		machines = append(machines, machine)
	}
	s.mu.Unlock()
	sort.Slice(machines, func(i, j int) bool { return machines[i].Snapshot.DeviceID < machines[j].Snapshot.DeviceID })
	return fleet{machines, time.Now().UTC()}
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.fleet())
}

func (s *Server) receive(w http.ResponseWriter, r *http.Request) {
	var snapshot report.Snapshot
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&snapshot); err != nil {
		http.Error(w, "Invalid report.", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		http.Error(w, "Expected one report.", http.StatusBadRequest)
		return
	}
	snapshot.DeviceID = identity.Normalize(snapshot.DeviceID)
	if !validID(snapshot.DeviceID) || snapshot.SchemaVersion != report.SchemaVersion || snapshot.ReportedAt.IsZero() {
		http.Error(w, "Invalid report identity or version.", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, exists := s.registry.Devices[snapshot.DeviceID]
	token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !exists || credential.RevokedAt != nil || !bearer || subtle.ConstantTimeCompare([]byte(hashSecret(token)), []byte(credential.TokenHash)) != 1 {
		http.Error(w, "Invalid agent credential.", http.StatusUnauthorized)
		return
	}
	previous, exists := s.machines[snapshot.DeviceID]
	if snapshot.Hardware == nil {
		if !exists {
			http.Error(w, "Startup inventory required.", http.StatusConflict)
			return
		}
		snapshot.Hardware = previous.Snapshot.Hardware
	}
	machine := Machine{Snapshot: snapshot, ReceivedAt: time.Now().UTC()}
	if err := s.saveMachine(machine); err != nil {
		http.Error(w, "Could not save report.", http.StatusInternalServerError)
		return
	}
	s.machines[snapshot.DeviceID] = machine
	s.notifyLocked()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) loadLegacyMachines(directory string) error {
	entries, err := os.ReadDir(filepath.Join(directory, "machines"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(directory, "machines", entry.Name()))
		if err != nil {
			return err
		}
		var machine Machine
		if err := json.Unmarshal(data, &machine); err != nil {
			return fmt.Errorf("load %s: %w", entry.Name(), err)
		}
		machine.Snapshot.DeviceID = identity.Normalize(machine.Snapshot.DeviceID)
		if !validID(machine.Snapshot.DeviceID) || machine.Snapshot.Hardware == nil {
			return fmt.Errorf("invalid persisted machine %s", entry.Name())
		}
		if credential, ok := s.registry.Devices[machine.Snapshot.DeviceID]; ok && credential.RevokedAt == nil {
			s.machines[machine.Snapshot.DeviceID] = machine
		}
	}

	return nil
}
