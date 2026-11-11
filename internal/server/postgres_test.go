package server

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func testDatabase(t *testing.T) string {
	t.Helper()
	connection := os.Getenv("HAKO_TEST_DATABASE_URL")
	if connection == "" {
		t.Fatal("PostgreSQL integration tests require HAKO_TEST_DATABASE_URL; use make test")
	}
	config, err := pgx.ParseConfig(connection)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := stdlib.OpenDB(*config)
	name := "hako_test_" + randomSecret(8)
	if _, err := pool.ExecContext(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := pool.ExecContext(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		pool.Close()
	})
	// pgx accepts both URL and keyword connection strings.
	if strings.HasPrefix(connection, "postgres://") || strings.HasPrefix(connection, "postgresql://") {
		parsed, err := url.Parse(connection)
		if err != nil {
			t.Fatal(err)
		}
		parsed.Path = "/" + name
		return parsed.String()
	}
	return connection + " dbname=" + name
}

func restartServer(t *testing.T, previous *Server, origin ...string) *Server {
	t.Helper()
	if err := previous.Close(); err != nil {
		t.Fatal(err)
	}
	external := ""
	if len(origin) > 0 {
		external = origin[0]
	}
	s, err := NewWithOrigin(previous.directory, previous.databaseURL, external)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
