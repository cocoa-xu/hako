package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func listen(ctx context.Context, addresses []string) ([]net.Listener, []string, error) {
	var listeners []net.Listener
	var hosts []string
	success := false
	defer func() {
		if !success {
			for _, listener := range listeners {
				_ = listener.Close()
			}
		}
	}()
	if len(addresses) == 0 {
		return nil, nil, fmt.Errorf("at least one listen address is required")
	}
	config := net.ListenConfig{}
	for _, address := range addresses {
		host, _, err := net.SplitHostPort(address)
		ip := net.ParseIP(host)
		if err != nil || ip == nil {
			return nil, nil, fmt.Errorf("listen address %q must specify an IP address and port", address)
		}
		network := "tcp6"
		if ip.To4() != nil {
			network = "tcp4"
		}
		listener, err := config.Listen(ctx, network, address)
		if err != nil {
			return nil, nil, err
		}
		listeners = append(listeners, listener)
		hosts = append(hosts, ip.String())
	}
	success = true
	return listeners, hosts, nil
}

func (s *Server) ListenAndServe(ctx context.Context, addresses []string) error {
	listeners, hosts, err := listen(ctx, addresses)
	if err != nil {
		return err
	}
	return s.serve(ctx, listeners, hosts)
}

func (s *Server) serve(ctx context.Context, listeners []net.Listener, hosts []string) error {
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	if len(s.registry.Devices) == 0 {
		if _, err := os.Stat(filepath.Join(s.directory, "hako.db")); err == nil {
			var imported string
			if err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='legacy_imported'").Scan(&imported); err != nil {
				return fmt.Errorf("SQLite state found: export it and run hako import before starting the PostgreSQL receiver")
			}
		}
	}
	httpServer := &http.Server{
		BaseContext: func(net.Listener) context.Context { return ctx },
		Handler:     s.Handler(hosts...), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second,
		IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10,
	}
	defer httpServer.Close()
	results := make(chan error, len(listeners))
	for _, listener := range listeners {
		go func() { results <- httpServer.Serve(listener) }()
		slog.Info("Hako is listening", "url", "http://"+listener.Addr().String())
	}
	databaseCheck := time.NewTicker(5 * time.Second)
	defer databaseCheck.Stop()
	for {
		select {
		case <-databaseCheck.C:
			check, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := s.db.PingContext(check)
			cancel()
			if err != nil {
				return fmt.Errorf("PostgreSQL connection lost; restart the receiver to reconnect safely")
			}
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return httpServer.Shutdown(shutdown)
		case err := <-results:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}
	}
}
