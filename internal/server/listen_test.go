package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeMultipleListenersAndShutDownTogether(t *testing.T) {
	s, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listeners, hosts, err := listen(ctx, []string{"127.0.0.1:0", "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	addresses := []string{listeners[0].Addr().String(), listeners[1].Addr().String()}
	done := make(chan error, 1)
	go func() { done <- s.serve(ctx, listeners, hosts) }()
	client := &http.Client{Timeout: 2 * time.Second}
	for _, address := range addresses {
		response, err := client.Get("http://" + address + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatal(response.StatusCode)
		}
	}
	client.CloseIdleConnections()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listeners did not shut down")
	}
	for _, address := range addresses {
		connection, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			connection.Close()
			t.Fatalf("listener %s is still open", address)
		}
	}
}

func TestListenFailureClosesEarlierListeners(t *testing.T) {
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reservation.Addr().String()
	reservation.Close()
	if _, _, err := listen(context.Background(), []string{address, "invalid-address"}); err == nil {
		t.Fatal("invalid listener was accepted")
	}
	rebound, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("earlier listener was leaked: %v", err)
	}
	rebound.Close()
}

func TestListenRequiresExplicitIPAddresses(t *testing.T) {
	for _, address := range []string{"", ":8787", "localhost:8787"} {
		if _, _, err := listen(context.Background(), []string{address}); err == nil {
			t.Fatalf("accepted %q", address)
		}
	}
	if _, _, err := listen(context.Background(), nil); err == nil {
		t.Fatal("accepted no listeners")
	}
}

func TestWildcardListener(t *testing.T) {
	listeners, hosts, err := listen(context.Background(), []string{"0.0.0.0:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer listeners[0].Close()
	if len(hosts) != 1 || hosts[0] != "0.0.0.0" {
		t.Fatal("wildcard listener lost its host policy")
	}
	_, port, _ := net.SplitHostPort(listeners[0].Addr().String())
	connection, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
}
