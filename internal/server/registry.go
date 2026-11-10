package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hako/internal/agent"
	"hako/internal/identity"
	"unicode"
	"unicode/utf8"
)

const maxEnrollmentTTL = 24 * time.Hour

type enrollment struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	DeviceID  string    `json:"device_id,omitempty"`
	Revoked   bool      `json:"revoked,omitempty"`
}

type credential struct {
	DisplayName string     `json:"display_name,omitempty"`
	TokenHash   string     `json:"token_hash"`
	CreatedAt   time.Time  `json:"created_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

type registry struct {
	AdminKeyHash string                `json:"admin_key_hash"`
	Sessions     map[string]time.Time  `json:"sessions"`
	Enrollments  map[string]enrollment `json:"enrollments"`
	Devices      map[string]credential `json:"devices"`
}

func randomSecret(size int) string {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value)
}

func hashSecret(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func (s *Server) loadLegacyRegistry() error {
	s.registry = registry{Enrollments: map[string]enrollment{}, Devices: map[string]credential{}, Sessions: map[string]time.Time{}}
	data, err := os.ReadFile(filepath.Join(s.directory, "registry.json"))
	if errors.Is(err, os.ErrNotExist) {
		entries, err := os.ReadDir(filepath.Join(s.directory, "agents"))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(s.directory, "agents", entry.Name()))
			if err != nil {
				return err
			}
			var config agent.Config
			if json.Unmarshal(data, &config) != nil || config.Validate() != nil {
				return fmt.Errorf("invalid legacy device credential")
			}
			s.registry.Devices[identity.Normalize(config.DeviceID)] = credential{TokenHash: hashSecret(config.Token), CreatedAt: time.Now().UTC()}
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &s.registry); err != nil {
		return fmt.Errorf("invalid device registry: %w", err)
	}
	if s.registry.Sessions == nil {
		s.registry.Sessions = map[string]time.Time{}
	}
	if s.registry.Enrollments == nil || s.registry.Devices == nil {
		return fmt.Errorf("incomplete device registry")
	}
	for id, device := range s.registry.Devices {
		normalized := identity.Normalize(id)
		if normalized != id {
			delete(s.registry.Devices, id)
			s.registry.Devices[normalized] = device
		}
	}
	for hash, item := range s.registry.Enrollments {
		item.DeviceID = identity.Normalize(item.DeviceID)
		s.registry.Enrollments[hash] = item
	}
	return nil
}

func (s *Server) copyRegistry() registry {
	next := registry{AdminKeyHash: s.registry.AdminKeyHash, Enrollments: map[string]enrollment{}, Devices: map[string]credential{}, Sessions: map[string]time.Time{}}
	for k, v := range s.registry.Sessions {
		next.Sessions[k] = v
	}
	for k, v := range s.registry.Enrollments {
		next.Enrollments[k] = v
	}
	for k, v := range s.registry.Devices {
		next.Devices[k] = v
	}
	return next
}

func decodeBody(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}

func (s *Server) createEnrollment(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Label      string `json:"label"`
		TTLSeconds int64  `json:"ttl_seconds"`
	}
	if decodeBody(w, r, &request) != nil || !validDisplayName(request.Label) || request.TTLSeconds < 60 || request.TTLSeconds > int64(maxEnrollmentTTL.Seconds()) {
		http.Error(w, "Use a label of at most 80 characters without control characters and a lifetime of 60 to 86400 seconds.", http.StatusBadRequest)
		return
	}
	key := randomSecret(32)
	now := time.Now().UTC()
	item := enrollment{ID: randomSecret(16), Label: strings.TrimSpace(request.Label), CreatedAt: now, ExpiresAt: now.Add(time.Duration(request.TTLSeconds) * time.Second)}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyRegistry()
	next.Enrollments[hashSecret(key)] = item
	if err := s.saveRegistry(next); err != nil {
		http.Error(w, "Could not save enrollment.", 500)
		return
	}
	s.registry = next
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(struct {
		enrollment
		Key string `json:"key"`
	}{item, key})
}

func (s *Server) listEnrollments(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]enrollment, 0, len(s.registry.Enrollments))
	for _, item := range s.registry.Enrollments {
		items = append(items, item)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}

func (s *Server) revokeEnrollment(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyRegistry()
	for hash, item := range next.Enrollments {
		if item.ID != r.PathValue("id") {
			continue
		}
		item.Revoked = true
		next.Enrollments[hash] = item
		if err := s.saveRegistry(next); err != nil {
			http.Error(w, "Could not revoke enrollment.", 500)
			return
		}
		s.registry = next
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DeviceID  string `json:"device_id"`
		TokenHash string `json:"token_hash"`
	}
	if decodeBody(w, r, &request) != nil || !validID(request.DeviceID) || len(request.TokenHash) != 64 || strings.Trim(request.TokenHash, "0123456789abcdef") != "" {
		http.Error(w, "Invalid registration.", http.StatusBadRequest)
		return
	}
	key, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !bearer || len(key) != 64 {
		http.Error(w, "Invalid enrollment key.", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := hashSecret(key)
	item, exists := s.registry.Enrollments[hash]
	if !exists || item.Revoked {
		http.Error(w, "Invalid enrollment key.", http.StatusUnauthorized)
		return
	}
	if item.DeviceID != "" {
		device := s.registry.Devices[item.DeviceID]
		if item.DeviceID == request.DeviceID && device.TokenHash == request.TokenHash && device.RevokedAt == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "Enrollment key already used.", http.StatusConflict)
		return
	}
	if !time.Now().Before(item.ExpiresAt) {
		http.Error(w, "Enrollment key expired.", http.StatusGone)
		return
	}
	if _, exists := s.registry.Devices[request.DeviceID]; exists {
		http.Error(w, "Device identity already registered.", http.StatusConflict)
		return
	}
	next := s.copyRegistry()
	item.DeviceID = request.DeviceID
	next.Enrollments[hash] = item
	next.Devices[request.DeviceID] = credential{TokenHash: request.TokenHash, CreatedAt: time.Now().UTC(), DisplayName: item.Label}
	if err := s.saveRegistry(next); err != nil {
		http.Error(w, "Could not register device.", 500)
		return
	}
	s.registry = next
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revokeMachine(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.PathValue("id")
	device, exists := s.registry.Devices[id]
	if !exists {
		http.NotFound(w, r)
		return
	}
	next := s.copyRegistry()
	now := time.Now().UTC()
	device.RevokedAt = &now
	next.Devices[id] = device
	if err := s.saveRegistry(next); err != nil {
		http.Error(w, "Could not revoke device.", 500)
		return
	}
	s.registry = next
	delete(s.machines, id)
	// The tombstone also prevents loading an old snapshot after a crash.
	s.notifyLocked()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) notifyLocked() {
	for subscriber := range s.subscribers {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}

func (s *Server) renameMachine(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DisplayName string `json:"display_name"`
	}
	if decodeBody(w, r, &request) != nil || !validDisplayName(request.DisplayName) {
		http.Error(w, "Use at most 80 characters without control characters.", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.PathValue("id")
	device, exists := s.registry.Devices[id]
	if !exists || device.RevokedAt != nil {
		http.NotFound(w, r)
		return
	}
	next := s.copyRegistry()
	device.DisplayName = strings.TrimSpace(request.DisplayName)
	next.Devices[id] = device
	if err := s.saveRegistry(next); err != nil {
		http.Error(w, "Could not save the display name.", 500)
		return
	}
	s.registry = next
	s.notifyLocked()
	w.WriteHeader(http.StatusNoContent)
}

func validDisplayName(name string) bool {
	return utf8.ValidString(name) && utf8.RuneCountInString(name) <= 80 && !strings.ContainsFunc(name, unicode.IsControl)
}
