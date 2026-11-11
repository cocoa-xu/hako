package server

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/001_initial.sql
var schema string

const databaseLockID int64 = 1751214959

func (s *Server) openDatabase(databaseURL string) error {
	if strings.TrimSpace(databaseURL) == "" {
		return fmt.Errorf("PostgreSQL is required: set HAKO_DATABASE_URL or --database-url-file")
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("invalid PostgreSQL connection configuration")
	}
	config.ConnectTimeout = 5 * time.Second
	config.RuntimeParams["application_name"] = "hako"
	config.RuntimeParams["synchronous_commit"] = "on"
	config.RuntimeParams["statement_timeout"] = "10000"
	config.RuntimeParams["lock_timeout"] = "5000"
	config.RuntimeParams["idle_in_transaction_session_timeout"] = "15000"
	s.pool = stdlib.OpenDB(*config)
	s.pool.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.db, err = s.pool.Conn(ctx)
	if err != nil {
		s.pool.Close()
		return fmt.Errorf("could not connect to PostgreSQL; check connectivity and credentials")
	}
	success := false
	defer func() {
		if !success {
			_ = s.Close()
		}
	}()
	var locked bool
	if err := s.db.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", databaseLockID).Scan(&locked); err != nil {
		return fmt.Errorf("could not acquire the PostgreSQL receiver lock")
	}
	if !locked {
		return fmt.Errorf("another Hako server owns this PostgreSQL database")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version BIGINT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())"); err != nil {
		return err
	}
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return fmt.Errorf("database schema %d is newer than this server supports", version)
	}
	if version == 0 {
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES(1)"); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	success = true
	return nil
}

// Close releases the database connection and its exclusive receiver lock.
func (s *Server) Close() error {
	if s.db != nil {
		_ = s.db.Close()
	}
	if s.pool != nil {
		return s.pool.Close()
	}
	return nil
}

func optionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}
func optionalID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func writeRegistry(ctx context.Context, tx *sql.Tx, value registry) error {
	for id, device := range value.Devices {
		_, err := tx.ExecContext(ctx, `INSERT INTO devices(id,token_hash,display_name,created_at,revoked_at) VALUES($1,$2,$3,$4,$5)
   ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,revoked_at=excluded.revoked_at`, id, device.TokenHash, device.DisplayName, device.CreatedAt, optionalTime(device.RevokedAt))
		if err != nil {
			return err
		}
	}
	for hash, item := range value.Enrollments {
		_, err := tx.ExecContext(ctx, `INSERT INTO enrollments(key_hash,id,label,created_at,expires_at,device_id,revoked) VALUES($1,$2,$3,$4,$5,$6,$7)
   ON CONFLICT(key_hash) DO UPDATE SET device_id=excluded.device_id,revoked=excluded.revoked`, hash, item.ID, item.Label, item.CreatedAt, item.ExpiresAt, optionalID(item.DeviceID), item.Revoked)
		if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return err
	}
	for hash, expires := range value.Sessions {
		if _, err := tx.ExecContext(ctx, "INSERT INTO sessions(token_hash,expires_at) VALUES($1,$2)", hash, expires); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('admin_key_hash',$1) ON CONFLICT(key) DO UPDATE SET value=excluded.value", value.AdminKeyHash); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM snapshots WHERE device_id IN (SELECT id FROM devices WHERE revoked_at IS NOT NULL)")
	return err
}

func (s *Server) saveRegistry(value registry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeRegistry(ctx, tx, value); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) loadRegistry() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	value := registry{Devices: map[string]credential{}, Enrollments: map[string]enrollment{}, Sessions: map[string]time.Time{}}
	rows, err := s.db.QueryContext(ctx, "SELECT id,token_hash,display_name,created_at,revoked_at FROM devices")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var device credential
		var created time.Time
		var revoked sql.NullTime
		if err := rows.Scan(&id, &device.TokenHash, &device.DisplayName, &created, &revoked); err != nil {
			rows.Close()
			return err
		}
		device.CreatedAt = created
		if revoked.Valid {
			at := revoked.Time
			device.RevokedAt = &at
		}
		value.Devices[id] = device
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.db.QueryContext(ctx, "SELECT key_hash,id,label,created_at,expires_at,device_id,revoked FROM enrollments")
	if err != nil {
		return err
	}
	for rows.Next() {
		var hash string
		var item enrollment
		var created, expires time.Time
		var device sql.NullString
		if err := rows.Scan(&hash, &item.ID, &item.Label, &created, &expires, &device, &item.Revoked); err != nil {
			rows.Close()
			return err
		}
		item.CreatedAt = created
		item.ExpiresAt = expires
		item.DeviceID = device.String
		value.Enrollments[hash] = item
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = s.db.QueryContext(ctx, "SELECT token_hash,expires_at FROM sessions")
	if err != nil {
		return err
	}
	for rows.Next() {
		var hash string
		var expires time.Time
		if err := rows.Scan(&hash, &expires); err != nil {
			rows.Close()
			return err
		}
		value.Sessions[hash] = expires
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	err = s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='admin_key_hash'").Scan(&value.AdminKeyHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	s.registry = value
	return nil
}

func (s *Server) saveMachine(machine Machine) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	payload, err := json.Marshal(machine)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO snapshots(device_id,payload) VALUES($1,$2) ON CONFLICT(device_id) DO UPDATE SET payload=excluded.payload", machine.Snapshot.DeviceID, string(payload))
	return err
}

func (s *Server) loadMachines() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	machines := map[string]Machine{}
	rows, err := s.db.QueryContext(ctx, "SELECT payload FROM snapshots JOIN devices ON devices.id=snapshots.device_id WHERE devices.revoked_at IS NULL")
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

func (s *Server) importLegacy(directory string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.loadLegacyRegistry(directory); err != nil {
		return err
	}
	if err := s.loadLegacyMachines(directory); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM devices) + (SELECT count(*) FROM enrollments) + (SELECT count(*) FROM snapshots) + (SELECT count(*) FROM sessions)").Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("import requires an empty PostgreSQL database")
	}
	if s.registry.AdminKeyHash != "" && s.registry.AdminKeyHash != hashSecret(s.adminKey) {
		return fmt.Errorf("copy the original admin.key into the server data directory before importing")
	}
	s.registry.AdminKeyHash = hashSecret(s.adminKey)
	if err := writeRegistry(ctx, tx, s.registry); err != nil {
		return err
	}
	for id, machine := range s.machines {
		payload, err := json.Marshal(machine)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO snapshots(device_id,payload) VALUES($1,$2)", id, string(payload)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('legacy_imported','1') ON CONFLICT(key) DO UPDATE SET value=excluded.value"); err != nil {
		return err
	}
	return tx.Commit()
}
