package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cocoa-xu/hako/internal/report"
	"github.com/coder/websocket"
)

func TestNginxProxyStreamsAndLegacyReports(t *testing.T) {
	nginx := os.Getenv("HAKO_TEST_NGINX")
	if nginx == "" {
		t.Skip("set HAKO_TEST_NGINX to exercise the deployment example")
	}
	s, config := setup(t)
	s.publicURL = &url.URL{Scheme: "https", Host: "hako.example.com"}
	handler := s.Handler()
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "hako.example.com" || r.Header.Get("CF-Access-Client-Id") != "worker.access" || r.Header.Get("CF-Access-Client-Secret") != "test-secret" {
			t.Error("proxy lost the original host or Access credentials")
		}
		handler.ServeHTTP(w, r)
	}))
	defer receiver.Close()
	directory, err := os.MkdirTemp("/tmp", "hako-proxy-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	socket := filepath.Join(directory, "proxy.sock")
	example, err := os.ReadFile("../../deploy/nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	proxy := strings.ReplaceAll(string(example), "127.0.0.1:8787", receiver.Listener.Addr().String())
	proxy = strings.ReplaceAll(proxy, "listen 127.0.0.1:8080;", "listen unix:"+socket+";")
	configuration := fmt.Sprintf(`daemon off;
master_process off;
pid %s/nginx.pid;
error_log stderr;
events {}
http {
    access_log off;
    client_body_temp_path client_body_temp;
    proxy_temp_path proxy_temp;
    fastcgi_temp_path fastcgi_temp;
    uwsgi_temp_path uwsgi_temp;
    scgi_temp_path scgi_temp;
    %s
}
`, directory, proxy)
	path := filepath.Join(directory, "nginx.conf")
	if err := os.WriteFile(path, []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(nginx, "-e", "stderr", "-p", directory, "-c", path, "-t").CombinedOutput(); err != nil {
		t.Fatalf("nginx configuration rejected: %v\n%s", err, output)
	}
	process := exec.Command(nginx, "-e", "stderr", "-p", directory, "-c", path)
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = process.Process.Kill(); _ = process.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("nginx did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+config.Token)
	headers.Set("X-Hako-Device-ID", config.DeviceID)
	headers.Set("CF-Access-Client-Id", "worker.access")
	headers.Set("CF-Access-Client-Secret", "test-secret")
	connection, _, err := websocket.Dial(ctx, "http://hako.example.com/api/reports", &websocket.DialOptions{
		HTTPClient: client, HTTPHeader: headers, Subprotocols: []string{report.StreamProtocol},
	})
	if err != nil {
		t.Fatal("nginx WebSocket upgrade failed", err)
	}
	defer connection.CloseNow()
	hardware := report.OK(report.Hardware{ModelName: "Test Mac"})
	snapshot := report.Snapshot{SchemaVersion: report.SchemaVersion, DeviceID: config.DeviceID, ReportedAt: time.Now().UTC(), Hardware: &hardware}
	for sequence := uint64(1); sequence <= 3; sequence++ {
		if status := streamReport(t, connection, sequence, snapshot); status != http.StatusNoContent {
			t.Fatal("proxied report failed", status)
		}
		snapshot.Hardware = nil
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://hako.example.com/api/reports", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header = headers
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("nginx legacy report failed", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal("nginx legacy report rejected", response.StatusCode)
	}
}
