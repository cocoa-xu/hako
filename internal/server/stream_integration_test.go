package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cocoa-xu/hako/internal/agent"
	"github.com/cocoa-xu/hako/internal/report"
)

type streamCollector struct {
	inventories atomic.Int64
	samples     atomic.Int64
}

func (c *streamCollector) Inventory(context.Context) report.Section[report.Hardware] {
	c.inventories.Add(1)
	return report.OK(report.Hardware{ModelName: "Test Mac"})
}

func (c *streamCollector) Sample(_ context.Context, id string) report.Snapshot {
	c.samples.Add(1)
	return report.Snapshot{SchemaVersion: report.SchemaVersion, DeviceID: id, ReportedAt: time.Now().UTC()}
}

func TestAgentRenewsAnExpiredConnection(t *testing.T) {
	s, config := setup(t)
	var requests atomic.Int64
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		s.receiveStreamWithTiming(w, r, streamTiming{
			lease: 100 * time.Millisecond, heartbeatInterval: 20 * time.Millisecond, heartbeatTimeout: time.Second,
		})
	}))
	defer receiver.Close()
	config.Server = receiver.URL
	collector := &streamCollector{}
	worker, err := agent.New(config, collector)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, time.Second) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	started := time.Now()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("new samples did not resume after the connection expired")
		case <-ticker.C:
			s.mu.Lock()
			machine := s.machines[config.DeviceID]
			s.mu.Unlock()
			if machine.Snapshot.ReportedAt.Sub(started) >= 900*time.Millisecond {
				if requests.Load() != 2 || collector.inventories.Load() != 1 || machine.Snapshot.Hardware == nil {
					t.Fatal("reconnect did not preserve inventory or limit handshakes")
				}
				return
			}
		}
	}
}

func TestStreamRenewsAfterLease(t *testing.T) {
	if os.Getenv("HAKO_TEST_STREAM_SOAK") == "" {
		t.Skip("set HAKO_TEST_STREAM_SOAK to test a full five-minute connection lease")
	}
	s, config := setup(t)
	var requests atomic.Int64
	handler := s.Handler()
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/reports" {
			requests.Add(1)
			if r.Method != http.MethodGet {
				t.Error("continuous worker fell back to HTTP reports")
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer receiver.Close()
	config.Server = receiver.URL
	collector := &streamCollector{}
	worker, err := agent.New(config, collector)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), streamLease+10*time.Second)
	defer cancel()
	if err := worker.Run(ctx, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || collector.inventories.Load() != 1 || collector.samples.Load() < 150 {
		t.Fatalf("requests=%d inventories=%d samples=%d", requests.Load(), collector.inventories.Load(), collector.samples.Load())
	}
	s.mu.Lock()
	machine := s.machines[config.DeviceID]
	s.mu.Unlock()
	if machine.ReceivedAt.Sub(started) < streamLease || machine.Snapshot.Hardware == nil {
		t.Fatal("reports did not resume after the connection lease")
	}
}
