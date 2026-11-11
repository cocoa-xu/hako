package server

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cocoa-xu/hako/internal/agent"
	"github.com/cocoa-xu/hako/internal/identity"
	"github.com/cocoa-xu/hako/internal/report"
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
	databaseURL := testDatabase(t)
	s, err := New(directory, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ImportLegacy(directory); err != nil {
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
	s, err = New(directory, databaseURL)
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
	s, err = New(directory, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.machines) != 0 {
		t.Fatal("legacy files reimported a revoked machine")
	}
	var snapshots int
	if err := s.db.QueryRowContext(context.Background(), "SELECT count(*) FROM snapshots WHERE device_id=$1", id).Scan(&snapshots); err != nil || snapshots != 0 {
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
	var synchronous string
	if err := s.db.QueryRowContext(context.Background(), "SHOW synchronous_commit").Scan(&synchronous); err != nil || synchronous != "on" {
		t.Fatal("durable commits disabled", err)
	}
	if _, err := s.db.ExecContext(context.Background(), "INSERT INTO snapshots(device_id,payload) VALUES($1,'{}')", identity.New()); err == nil {
		t.Fatal("foreign key accepted an unknown device")
	}
	var version int
	if err := s.db.QueryRowContext(context.Background(), "SELECT max(version) FROM schema_migrations").Scan(&version); err != nil || version != 1 {
		t.Fatal("migration version missing", err)
	}
}

func TestDatabaseOwnershipAndNewerSchema(t *testing.T) {
	s, _ := setup(t)
	if other, err := New(t.TempDir(), s.databaseURL); err == nil {
		other.Close()
		t.Fatal("second receiver acquired the database")
	}
	if _, err := s.db.ExecContext(context.Background(), "INSERT INTO schema_migrations(version) VALUES(999)"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if other, err := New(s.directory, s.databaseURL); err == nil {
		other.Close()
		t.Fatal("newer schema accepted")
	}
}

func TestImportRollsBackAndRefusesExistingState(t *testing.T) {
	s, config := setup(t)
	original := s.registry.Devices[config.DeviceID]
	source := t.TempDir()
	if err := Prepare(source); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(registry{AdminKeyHash: hashSecret(s.adminKey), Devices: map[string]credential{}, Enrollments: map[string]enrollment{}, Sessions: map[string]time.Time{}})
	if err := os.WriteFile(filepath.Join(source, "registry.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportLegacy(source); err == nil {
		t.Fatal("import overwrote a registered device")
	}
	if s.registry.Devices[config.DeviceID].TokenHash != original.TokenHash {
		t.Fatal("failed import changed live state")
	}
}

func TestSQLiteExportImportsCredentialsAndRevocations(t *testing.T) {
	directory := t.TempDir()
	s, err := New(directory, testDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id := identity.New()
	token := randomSecret(32)
	key := randomSecret(32)
	sqlitePath := filepath.Join(t.TempDir(), "prototype.db")
	script := `import sqlite3,sys,time
p,device,token_hash,key_hash,admin_hash=sys.argv[1:]
now=time.time_ns()
with sqlite3.connect(p) as db:
 db.executescript('PRAGMA user_version=1; CREATE TABLE settings(key,value); CREATE TABLE devices(id,token_hash,display_name,created_at,revoked_at); CREATE TABLE enrollments(key_hash,id,label,created_at,expires_at,device_id,revoked); CREATE TABLE sessions(token_hash,expires_at); CREATE TABLE snapshots(device_id,payload);')
 db.execute('INSERT INTO settings VALUES(?,?)',('admin_key_hash',admin_hash))
 db.execute('INSERT INTO devices VALUES(?,?,?,?,?)',(device,token_hash,'Test Mac',now,now))
 db.execute('INSERT INTO enrollments VALUES(?,?,?,?,?,?,?)',(key_hash,'0123456789abcdef0123456789abcdef','Test Mac',now,now+900000000000,device,0))
`
	command := exec.Command("python3", "-c", script, sqlitePath, id, hashSecret(token), hashSecret(key), hashSecret(s.adminKey))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, output)
	}
	exported := filepath.Join(t.TempDir(), "export")
	command = exec.Command("python3", "../../scripts/export-sqlite.py", "--database", sqlitePath, "--output-dir", exported)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("export: %v %s", err, output)
	}
	if err := s.ImportLegacy(exported); err != nil {
		t.Fatal(err)
	}
	if s.registry.Devices[id].TokenHash != hashSecret(token) || s.registry.Devices[id].RevokedAt == nil {
		t.Fatal("revoked credential changed during migration")
	}
	response := requestAPI(t, s, "POST", "/api/enroll", key, map[string]string{"device_id": id, "token_hash": hashSecret(token)})
	if response.Code != http.StatusConflict {
		t.Fatal("migration resurrected a revoked device", response.Code)
	}
}

func TestLostDatabaseSessionCannotReconnectWithStaleState(t *testing.T) {
	s, config := setup(t)
	var pid int
	if err := s.db.QueryRowContext(context.Background(), "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	connection, err := pgx.ParseConfig(s.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*connection)
	defer admin.Close()
	if _, err := admin.Exec("SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatal(err)
	}
	healthy, err := New(s.directory, s.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer healthy.Close()
	if response := requestAPI(t, healthy, "DELETE", "/api/machines/"+config.DeviceID, healthy.adminKey, nil); response.Code != 204 {
		t.Fatal(response.Code)
	}
	hardware := report.OK(report.Hardware{ModelName: "Test Mac"})
	snapshot := report.Snapshot{SchemaVersion: 1, DeviceID: config.DeviceID, ReportedAt: time.Now(), Hardware: &hardware}
	for i := 0; i < 2; i++ {
		if response := post(t, s, config, snapshot); response.Code != 500 {
			t.Fatal("stale receiver reused a lost database session", response.Code)
		}
	}
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest("GET", "http://127.0.0.1:8787/healthz", nil))
	if response.Code != 503 {
		t.Fatal("dead database reported healthy")
	}
	if response := post(t, healthy, config, snapshot); response.Code != 401 {
		t.Fatal("stale receiver undid revocation")
	}
}
