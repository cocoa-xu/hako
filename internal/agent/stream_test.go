package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cocoa-xu/hako/internal/report"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestThousandReportsUseOneAuthenticatedRequest(t *testing.T) {
	config := Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: strings.Repeat("a", 64), CloudflareAccess: &AccessServiceToken{ClientID: "worker.access", ClientSecret: "test-secret"}}
	var requests, reports atomic.Int64
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "GET" || r.URL.Path != "/api/reports" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer "+config.Token || r.Header.Get("CF-Access-Client-Id") != config.CloudflareAccess.ClientID || r.Header.Get("CF-Access-Client-Secret") != config.CloudflareAccess.ClientSecret {
			t.Error("stream handshake lost credentials or used the wrong endpoint")
		}
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{report.StreamProtocol}})
		if err != nil {
			return
		}
		defer connection.CloseNow()
		for {
			var message report.StreamReport
			if wsjson.Read(r.Context(), connection, &message) != nil {
				return
			}
			reports.Add(1)
			if err := wsjson.Write(r.Context(), connection, report.StreamAck{Sequence: message.Sequence, Status: 204}); err != nil {
				return
			}
		}
	}))
	defer receiver.Close()
	config.Server = receiver.URL
	a, err := New(config, &fakeCollector{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := a.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.close()
	for i := 0; i < 1000; i++ {
		if err := stream.send(ctx, a.collect(ctx)); err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != 1 || reports.Load() != 1000 || a.collector.(*fakeCollector).inventories != 1 {
		t.Fatalf("requests=%d reports=%d", requests.Load(), reports.Load())
	}
}

func TestStreamHandshakeFailuresDoNotFallBackOrFollowRedirects(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("credentials followed a redirect") }))
	defer destination.Close()
	for _, status := range []int{200, 302, 400, 401, 403, 404, 405, 426, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("worker fell back to HTTP POST")
				}
				w.Header().Set("Location", destination.URL)
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(status)
			}))
			defer receiver.Close()
			a, _ := New(Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: strings.Repeat("a", 64), Server: receiver.URL, CloudflareAccess: &AccessServiceToken{ClientID: "test.access", ClientSecret: "test-secret"}}, &fakeCollector{})
			stream, err := a.connect(context.Background())
			if err == nil {
				stream.close()
				t.Fatal("failed handshake accepted")
			}
			switch status {
			case 302, 401, 403:
				if !errors.Is(err, ErrUnauthorized) {
					t.Fatal(err)
				}
			case 429, 503:
				var retry *retryAfterError
				if !errors.As(err, &retry) || retry.delay != 2*time.Minute {
					t.Fatal("Retry-After lost", err)
				}
			default:
				if !errors.Is(err, ErrStreamingUnsupported) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestStreamAcknowledgements(t *testing.T) {
	for _, status := range []int{204, 409, 401, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{report.StreamProtocol}})
				if err != nil {
					return
				}
				defer connection.CloseNow()
				var message report.StreamReport
				if wsjson.Read(r.Context(), connection, &message) != nil {
					return
				}
				_ = wsjson.Write(r.Context(), connection, report.StreamAck{Sequence: message.Sequence, Status: status})
				_, _, _ = connection.Read(r.Context())
			}))
			defer receiver.Close()
			a, _ := New(Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: strings.Repeat("a", 64), Server: receiver.URL}, &fakeCollector{})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			stream, err := a.connect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.close()
			err = stream.send(ctx, a.collect(ctx))
			if status == 204 && err != nil || status != 204 && err == nil {
				t.Fatal(status, err)
			}
			if status == 409 && !errors.Is(err, errInventoryRequired) {
				t.Fatal(err)
			}
			if status == 401 && !errors.Is(err, ErrUnauthorized) {
				t.Fatal(err)
			}
		})
	}
}

func TestStreamReadsHeartbeatsWhileSamplingIsIdle(t *testing.T) {
	pinged := make(chan error, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{report.StreamProtocol}})
		if err != nil {
			pinged <- err
			return
		}
		defer connection.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		go func() { pinged <- connection.Ping(ctx) }()
		_, _, _ = connection.Read(ctx)
	}))
	defer receiver.Close()
	a, _ := New(Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: strings.Repeat("a", 64), Server: receiver.URL}, &fakeCollector{})
	stream, err := a.connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.close()
	if err := <-pinged; err != nil {
		t.Fatal("idle worker did not answer heartbeat", err)
	}
}
