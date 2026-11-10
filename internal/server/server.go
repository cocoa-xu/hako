package server

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
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
	"hako/internal/report"
)

//go:embed web/*
var assets embed.FS

type Machine struct {
	Snapshot   report.Snapshot `json:"snapshot"`
	ReceivedAt time.Time       `json:"received_at"`
}

type Server struct {
	directory   string
	mu          sync.Mutex
	machines    map[string]Machine
	subscribers map[chan struct{}]struct{}
}

func validID(id string) bool { return len(id) == 32 && strings.Trim(id, "0123456789abcdef") == "" }

func Prepare(directory string) error {
	for _, path := range []string{directory, filepath.Join(directory, "agents"), filepath.Join(directory, "machines")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
	}
	return nil
}

func Enroll(directory, endpoint string) (string, error) {
	if err := Prepare(directory); err != nil {
		return "", err
	}
	var id [16]byte
	var token [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	config := agent.Config{DeviceID: hex.EncodeToString(id[:]), Token: hex.EncodeToString(token[:]), Server: endpoint}
	if err := config.Validate(); err != nil {
		return "", err
	}
	path := filepath.Join(directory, "agents", config.DeviceID+".json")
	return path, writeJSON(path, config)
}

func New(directory string) (*Server, error) {
	if err := Prepare(directory); err != nil {
		return nil, err
	}
	s := &Server{directory: directory, machines: make(map[string]Machine), subscribers: make(map[chan struct{}]struct{})}
	entries, err := os.ReadDir(filepath.Join(directory, "machines"))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(directory, "machines", entry.Name()))
		if err != nil {
			return nil, err
		}
		var machine Machine
		if err := json.Unmarshal(data, &machine); err != nil {
			return nil, fmt.Errorf("load %s: %w", entry.Name(), err)
		}
		if !validID(machine.Snapshot.DeviceID) || machine.Snapshot.Hardware == nil {
			return nil, fmt.Errorf("invalid persisted machine %s", entry.Name())
		}
		s.machines[machine.Snapshot.DeviceID] = machine
	}
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
	mux.HandleFunc("GET /api/machines", s.list)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("POST /api/reports", s.receive)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
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
		if host != "localhost" && (ip == nil || (!ip.IsLoopback() && !allowedHosts[ip.String()])) {
			http.Error(w, "This host is not configured for the receiver.", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host || u.Scheme != "http" {
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
	for _, machine := range s.machines {
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
	if !validID(snapshot.DeviceID) || snapshot.SchemaVersion != report.SchemaVersion || snapshot.ReportedAt.IsZero() {
		http.Error(w, "Invalid report identity or version.", http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(filepath.Join(s.directory, "agents", snapshot.DeviceID+".json"))
	var credential agent.Config
	if err != nil || json.Unmarshal(data, &credential) != nil || len(credential.Token) < 32 ||
		subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+credential.Token)) != 1 {
		http.Error(w, "Invalid agent credential.", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, exists := s.machines[snapshot.DeviceID]
	if snapshot.Hardware == nil {
		if !exists {
			http.Error(w, "Startup inventory required.", http.StatusConflict)
			return
		}
		snapshot.Hardware = previous.Snapshot.Hardware
	}
	machine := Machine{Snapshot: snapshot, ReceivedAt: time.Now().UTC()}
	if err := writeJSON(filepath.Join(s.directory, "machines", snapshot.DeviceID+".json"), machine); err != nil {
		http.Error(w, "Could not save report.", http.StatusInternalServerError)
		return
	}
	s.machines[snapshot.DeviceID] = machine
	for subscriber := range s.subscribers {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".hako-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
