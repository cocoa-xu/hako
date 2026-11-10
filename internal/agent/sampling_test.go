package agent

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"hako/internal/report"
)

type scheduledCollector struct {
	fakeCollector
	times []time.Time
}

func (c *scheduledCollector) Sample(_ context.Context, id string) report.Snapshot {
	c.times = append(c.times, time.Now())
	time.Sleep(200 * time.Millisecond)
	return report.Snapshot{DeviceID: id, ReportedAt: time.Now()}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSamplingKeepsCadenceWhileDeliveryIsBlocked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		collector := &scheduledCollector{}
		a, _ := New(Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: strings.Repeat("a", 32), Server: "http://127.0.0.1"}, collector)
		release := make(chan struct{})
		var sent []string
		a.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(r.Body)
			sent = append(sent, string(body))
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
		})
		done := make(chan error, 1)
		go func() { done <- a.Run(ctx, 2*time.Second) }()
		time.Sleep(5 * time.Second)
		synctest.Wait()
		if len(collector.times) != 3 || len(sent) != 1 {
			t.Fatalf("sampling blocked: %d samples, %d deliveries", len(collector.times), len(sent))
		}
		if collector.times[2].Sub(collector.times[0]) != 4*time.Second {
			t.Fatal("collection time was added to the interval")
		}
		close(release)
		synctest.Wait()
		if len(sent) != 2 || !strings.Contains(sent[1], collector.times[2].Add(200*time.Millisecond).Format(time.RFC3339Nano)) {
			t.Fatal("delivery did not skip superseded samples")
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if collector.inventories != 1 {
			t.Fatal("hardware was rescanned")
		}
	})
}
