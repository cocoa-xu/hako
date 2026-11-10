package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hako/internal/agent"
	"hako/internal/identity"
	"hako/internal/report"
)

func TestLegacyMigrationPreservesIdentityAndDisplayNameAcrossRestart(t *testing.T) {
	directory := t.TempDir()
	if err := Prepare(directory); err != nil {
		t.Fatal(err)
	}
	config := agent.Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: strings.Repeat("c", 64), Server: "http://127.0.0.1:8787"}
	hardware := report.OK(report.Hardware{ModelName: "Test Mac"})
	snapshot := report.Snapshot{SchemaVersion: report.SchemaVersion, DeviceID: config.DeviceID, ReportedAt: time.Now().UTC(), Hardware: &hardware, System: report.OK(report.System{Hostname: "original-hostname"})}
	for path, value := range map[string]any{
		filepath.Join(directory, "agents", config.DeviceID+".json"):   config,
		filepath.Join(directory, "machines", config.DeviceID+".json"): Machine{Snapshot: snapshot, ReceivedAt: time.Now().UTC()},
	} {
		data, _ := json.Marshal(value)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(directory)
	if err != nil {
		t.Fatal(err)
	}
	id := identity.Normalize(config.DeviceID)
	if !identity.Valid(id) || len(s.machines) != 1 || s.machines[id].Snapshot.System.Data.Hostname != "original-hostname" {
		t.Fatal("legacy machine lost")
	}
	if response := post(t, s, config, snapshot); response.Code != http.StatusNoContent {
		t.Fatal("legacy worker stopped reporting", response.Code)
	}
	if response := requestAPI(t, s, "PATCH", "/api/machines/"+id, s.adminKey, map[string]string{"display_name": "Build Mac"}); response.Code != 204 {
		t.Fatal(response.Code)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fleet := s.fleet()
	if len(fleet.Machines) != 1 || fleet.Machines[0].DisplayName != "Build Mac" || fleet.Machines[0].Snapshot.DeviceID != id || fleet.Machines[0].Snapshot.System.Data.Hostname != "original-hostname" {
		t.Fatal("rename changed the identity or hostname")
	}
	if response := requestAPI(t, s, "DELETE", "/api/machines/"+id, s.adminKey, nil); response.Code != 204 {
		t.Fatal(response.Code)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.machines) != 0 {
		t.Fatal("legacy files reimported a revoked machine")
	}
	var snapshots int
	if err := s.db.QueryRow("SELECT count(*) FROM snapshots WHERE device_id=?", id).Scan(&snapshots); err != nil || snapshots != 0 {
		t.Fatal("revoked snapshot retained", err)
	}
}

func TestDisplayNameValidationAndDatabaseSettings(t *testing.T) {
	s, config := setup(t)
	for _, name := range []string{strings.Repeat("x", 81), "name\nsecond line"} {
		if response := requestAPI(t, s, "PATCH", "/api/machines/"+config.DeviceID, s.adminKey, map[string]string{"display_name": name}); response.Code != 400 {
			t.Fatal("invalid display name accepted")
		}
	}
	var journal, check string
	var foreignKeys, synchronous int
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
		t.Fatal("WAL disabled", err)
	}
	if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatal("foreign keys disabled", err)
	}
	if err := s.db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil || synchronous != 2 {
		t.Fatal("durable sync disabled", err)
	}
	if err := s.db.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		t.Fatal("database integrity", err)
	}
}
