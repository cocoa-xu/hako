package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS devices (
 id TEXT PRIMARY KEY CHECK(length(id)=36), token_hash TEXT NOT NULL,
 display_name TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, revoked_at INTEGER
);
CREATE TABLE IF NOT EXISTS enrollments (
 key_hash TEXT PRIMARY KEY, id TEXT NOT NULL UNIQUE, label TEXT NOT NULL,
 created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL,
 device_id TEXT REFERENCES devices(id), revoked INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS sessions (token_hash TEXT PRIMARY KEY, expires_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS snapshots (
 device_id TEXT PRIMARY KEY REFERENCES devices(id), payload BLOB NOT NULL
);
PRAGMA user_version=1;
`

func (s *Server) openDatabase() error {
	path, err := filepath.Abs(filepath.Join(s.directory, "hako.db"))
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	parameters := url.Values{"_pragma": {"foreign_keys(1)", "busy_timeout(5000)", "synchronous(FULL)"}}
	location := url.URL{Scheme: "file", Path: path, RawQuery: parameters.Encode()}
	s.db, err = sql.Open("sqlite", location.String())
	if err != nil {
		return err
	}
	s.db.SetMaxOpenConns(1)
	s.db.SetMaxIdleConns(1)
	success := false
	defer func() {
		if !success {
			_ = s.db.Close()
		}
	}()
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return fmt.Errorf("database schema %d is newer than this server supports", version)
	}
	if _, err := s.db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return err
	}
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	var migrated string
	err = s.db.QueryRow("SELECT value FROM settings WHERE key='legacy_imported'").Scan(&migrated)
	if errors.Is(err, sql.ErrNoRows) {
		if err := s.importLegacy(); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	success = true
	return nil
}

// Close releases the database after the receiver has stopped.
func (s *Server) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func timestamp(value time.Time) int64     { return value.UnixNano() }
func fromTimestamp(value int64) time.Time { return time.Unix(0, value).UTC() }
func optionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return timestamp(*value)
}
func optionalID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func writeRegistry(tx *sql.Tx, value registry) error {
	for id, device := range value.Devices {
		_, err := tx.Exec(`INSERT INTO devices(id,token_hash,display_name,created_at,revoked_at) VALUES(?,?,?,?,?)
   ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,revoked_at=excluded.revoked_at`, id, device.TokenHash, device.DisplayName, timestamp(device.CreatedAt), optionalTime(device.RevokedAt))
		if err != nil {
			return err
		}
	}
	for hash, item := range value.Enrollments {
		_, err := tx.Exec(`INSERT INTO enrollments(key_hash,id,label,created_at,expires_at,device_id,revoked) VALUES(?,?,?,?,?,?,?)
   ON CONFLICT(key_hash) DO UPDATE SET device_id=excluded.device_id,revoked=excluded.revoked`, hash, item.ID, item.Label, timestamp(item.CreatedAt), timestamp(item.ExpiresAt), optionalID(item.DeviceID), item.Revoked)
		if err != nil {
			return err
		}
	}
	if _, err := tx.Exec("DELETE FROM sessions"); err != nil {
		return err
	}
	for hash, expires := range value.Sessions {
		if _, err := tx.Exec("INSERT INTO sessions(token_hash,expires_at) VALUES(?,?)", hash, timestamp(expires)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("INSERT INTO settings(key,value) VALUES('admin_key_hash',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", value.AdminKeyHash); err != nil {
		return err
	}
	_, err := tx.Exec("DELETE FROM snapshots WHERE device_id IN (SELECT id FROM devices WHERE revoked_at IS NOT NULL)")
	return err
}

func (s *Server) saveRegistry(value registry) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeRegistry(tx, value); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) loadRegistry() error {
	value := registry{Devices: map[string]credential{}, Enrollments: map[string]enrollment{}, Sessions: map[string]time.Time{}}
	rows, err := s.db.Query("SELECT id,token_hash,display_name,created_at,revoked_at FROM devices")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var device credential
		var created int64
		var revoked sql.NullInt64
		if err := rows.Scan(&id, &device.TokenHash, &device.DisplayName, &created, &revoked); err != nil {
			rows.Close()
			return err
		}
		device.CreatedAt = fromTimestamp(created)
		if revoked.Valid {
			at := fromTimestamp(revoked.Int64)
			device.RevokedAt = &at
		}
		value.Devices[id] = device
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.db.Query("SELECT key_hash,id,label,created_at,expires_at,device_id,revoked FROM enrollments")
	if err != nil {
		return err
	}
	for rows.Next() {
		var hash string
		var item enrollment
		var created, expires int64
		var device sql.NullString
		if err := rows.Scan(&hash, &item.ID, &item.Label, &created, &expires, &device, &item.Revoked); err != nil {
			rows.Close()
			return err
		}
		item.CreatedAt = fromTimestamp(created)
		item.ExpiresAt = fromTimestamp(expires)
		item.DeviceID = device.String
		value.Enrollments[hash] = item
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.db.Query("SELECT token_hash,expires_at FROM sessions")
	if err != nil {
		return err
	}
	for rows.Next() {
		var hash string
		var expires int64
		if err := rows.Scan(&hash, &expires); err != nil {
			rows.Close()
			return err
		}
		value.Sessions[hash] = fromTimestamp(expires)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	err = s.db.QueryRow("SELECT value FROM settings WHERE key='admin_key_hash'").Scan(&value.AdminKeyHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	s.registry = value
	return nil
}

func (s *Server) saveMachine(machine Machine) error {
	payload, err := json.Marshal(machine)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO snapshots(device_id,payload) VALUES(?,?) ON CONFLICT(device_id) DO UPDATE SET payload=excluded.payload", machine.Snapshot.DeviceID, payload)
	return err
}

func (s *Server) loadMachines() error {
	machines := map[string]Machine{}
	rows, err := s.db.Query("SELECT payload FROM snapshots JOIN devices ON devices.id=snapshots.device_id WHERE devices.revoked_at IS NULL")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		var machine Machine
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		if err := json.Unmarshal(payload, &machine); err != nil {
			return fmt.Errorf("invalid persisted snapshot: %w", err)
		}
		if !validID(machine.Snapshot.DeviceID) || machine.Snapshot.Hardware == nil {
			return fmt.Errorf("invalid persisted machine")
		}
		machines[machine.Snapshot.DeviceID] = machine
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.machines = machines
	return nil
}

func (s *Server) importLegacy() error {
	if err := s.loadLegacyRegistry(); err != nil {
		return err
	}
	if err := s.loadLegacyMachines(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeRegistry(tx, s.registry); err != nil {
		return err
	}
	for id, machine := range s.machines {
		payload, err := json.Marshal(machine)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO snapshots(device_id,payload) VALUES(?,?)", id, payload); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("INSERT INTO settings(key,value) VALUES('legacy_imported','1')"); err != nil {
		return err
	}
	return tx.Commit()
}
