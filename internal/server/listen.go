package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
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
		if err != nil || ip == nil || ip.IsUnspecified() {
			return nil, nil, fmt.Errorf("listen address %q must specify an individual IP address and port", address)
		}
		listener, err := config.Listen(ctx, "tcp", address)
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
	httpServer := &http.Server{
		Handler: s.Handler(hosts...), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second,
		IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10,
	}
	defer httpServer.Close()
	results := make(chan error, len(listeners))
	for _, listener := range listeners {
		go func() { results <- httpServer.Serve(listener) }()
		slog.Info("Hako is listening", "url", "http://"+listener.Addr().String())
	}
	select {
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
