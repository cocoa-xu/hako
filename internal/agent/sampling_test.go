package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cocoa-xu/hako/internal/report"
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

type testSender struct {
	write func(context.Context, report.Snapshot) error
}

func (sender *testSender) send(ctx context.Context, snapshot report.Snapshot) error {
	return sender.write(ctx, snapshot)
}

func (sender *testSender) close() {}

func TestSamplingKeepsCadenceWhileDeliveryIsBlocked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		collector := &scheduledCollector{}
		a, _ := New(Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: strings.Repeat("a", 32), Server: "http://127.0.0.1"}, collector)
		release := make(chan struct{})
		var sent []string
		sender := &testSender{write: func(ctx context.Context, snapshot report.Snapshot) error {
			sent = append(sent, snapshot.ReportedAt.Format(time.RFC3339Nano))
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		}}
		done := make(chan error, 1)
		go func() {
			done <- a.run(ctx, 2*time.Second, func(context.Context) (reportSender, error) { return sender, nil })
		}()
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

func TestReconnectBackoffKeepsSamplingAndCancelsPromptly(t *testing.T) {
	for _, failAfterConnect := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			collector := &scheduledCollector{}
			a, _ := New(Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: strings.Repeat("a", 32), Server: "http://127.0.0.1"}, collector)
			var attempts []time.Time
			done := make(chan error, 1)
			go func() {
				done <- a.run(ctx, 2*time.Second, func(context.Context) (reportSender, error) {
					attempts = append(attempts, time.Now())
					if failAfterConnect {
						return &testSender{write: func(context.Context, report.Snapshot) error { return errors.New("connection lost") }}, nil
					}
					return nil, errors.New("receiver unavailable")
				})
			}()
			time.Sleep(5 * time.Minute)
			synctest.Wait()
			if len(attempts) < 5 || len(attempts) > 20 || len(collector.times) < 150 {
				t.Fatalf("attempts=%d samples=%d", len(attempts), len(collector.times))
			}
			for i := 1; i < len(attempts); i++ {
				minimum := min(2*time.Second*time.Duration(1<<min(i-1, 5)), time.Minute) / 2
				if attempts[i].Sub(attempts[i-1]) < minimum {
					t.Fatal("reconnect did not back off")
				}
			}
			cancel()
			synctest.Wait()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReconnectHonorsRetryAfterAndStopsOnAuthenticationFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		a, _ := New(Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: strings.Repeat("a", 32), Server: "http://127.0.0.1"}, &fakeCollector{})
		var attempts atomic.Int64
		done := make(chan error, 1)
		go func() {
			done <- a.run(ctx, 2*time.Second, func(context.Context) (reportSender, error) {
				if attempts.Add(1) == 1 {
					return nil, &retryAfterError{status: 429, delay: time.Minute}
				}
				return nil, ErrUnauthorized
			})
		}()
		time.Sleep(59 * time.Second)
		synctest.Wait()
		if attempts.Load() != 1 {
			t.Fatal("Retry-After ignored")
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if err := <-done; !errors.Is(err, ErrUnauthorized) || attempts.Load() != 2 {
			t.Fatal("authentication failure retried", err, attempts.Load())
		}
	})
}

func TestStreamInventoryRequiresAcknowledgementAndSurvivesReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		collector := &fakeCollector{}
		a, _ := New(Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: strings.Repeat("a", 32), Server: "http://127.0.0.1"}, collector)
		var sent []report.Snapshot
		connections := 0
		err := a.run(ctx, 2*time.Second, func(context.Context) (reportSender, error) {
			connections++
			return &testSender{write: func(_ context.Context, snapshot report.Snapshot) error {
				sent = append(sent, snapshot)
				switch len(sent) {
				case 1:
					return errors.New("acknowledgement lost")
				case 3:
					return errInventoryRequired
				case 5:
					cancel()
				}
				return nil
			}}, nil
		})
		if err != nil || connections != 2 || collector.inventories != 1 || len(sent) != 5 {
			t.Fatalf("connections=%d scans=%d reports=%d error=%v", connections, collector.inventories, len(sent), err)
		}
		for i, want := range []bool{true, true, false, true, false} {
			if (sent[i].Hardware != nil) != want {
				t.Fatalf("report %d inventory inclusion is wrong", i)
			}
		}
		if !sent[0].Hardware.ObservedAt.Equal(sent[3].Hardware.ObservedAt) {
			t.Fatal("reconnect rescanned inventory")
		}
	})
}

func TestStreamRetainsSamplesWhenTheConnectionExpiresBetweenSamples(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		collector := &fakeCollector{}
		a, _ := New(Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: strings.Repeat("a", 32), Server: "http://127.0.0.1"}, collector)
		started := time.Now()
		var delivered []report.Snapshot
		done := make(chan error, 1)
		go func() {
			done <- a.run(ctx, 10*time.Minute, func(context.Context) (reportSender, error) {
				connected := time.Now()
				return &testSender{write: func(_ context.Context, snapshot report.Snapshot) error {
					if time.Since(connected) >= 5*time.Minute {
						return errStreamRenewal
					}
					delivered = append(delivered, snapshot)
					return nil
				}}, nil
			})
		}()
		time.Sleep(21 * time.Minute)
		synctest.Wait()
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if len(delivered) != 3 || collector.inventories != 1 {
			t.Fatalf("delivered=%d inventory scans=%d", len(delivered), collector.inventories)
		}
		for i, snapshot := range delivered {
			if snapshot.ReportedAt.Sub(started) != time.Duration(i)*10*time.Minute || snapshot.Hardware == nil {
				t.Fatalf("sample %d was lost or reconnected without inventory", i)
			}
		}
	})
}

func TestReconnectPrefersTheLatestQueuedSample(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		a, _ := New(Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: strings.Repeat("a", 32), Server: "http://127.0.0.1"}, &fakeCollector{})
		started := time.Now()
		var sent []report.Snapshot
		err := a.run(ctx, 2*time.Second, func(context.Context) (reportSender, error) {
			return &testSender{write: func(_ context.Context, snapshot report.Snapshot) error {
				sent = append(sent, snapshot)
				if len(sent) == 1 {
					time.Sleep(5 * time.Second)
					return errors.New("acknowledgement lost")
				}
				cancel()
				return nil
			}}, nil
		})
		if err != nil || len(sent) != 2 {
			t.Fatalf("sent=%d error=%v", len(sent), err)
		}
		if sent[1].ReportedAt.Sub(started) < 4*time.Second || sent[1].Hardware == nil {
			t.Fatal("reconnect replayed an obsolete sample or omitted inventory")
		}
	})
}

func TestMissingInventoryIsResentWithoutWaitingForAnotherSample(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		a, _ := New(Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: strings.Repeat("a", 32), Server: "http://127.0.0.1"}, &fakeCollector{})
		started := time.Now()
		var sent []report.Snapshot
		err := a.run(ctx, 10*time.Minute, func(context.Context) (reportSender, error) {
			return &testSender{write: func(_ context.Context, snapshot report.Snapshot) error {
				sent = append(sent, snapshot)
				switch len(sent) {
				case 2:
					return errInventoryRequired
				case 3:
					cancel()
				}
				return nil
			}}, nil
		})
		if err != nil || len(sent) != 3 || time.Since(started) != 10*time.Minute {
			t.Fatalf("sent=%d elapsed=%v error=%v", len(sent), time.Since(started), err)
		}
		if sent[1].Hardware != nil || sent[2].Hardware == nil || !sent[1].ReportedAt.Equal(sent[2].ReportedAt) {
			t.Fatal("inventory retry did not preserve the pending sample")
		}
	})
}

func TestFullInventoryRejectionBacksOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		a, _ := New(Config{DeviceID: "0123456789abcdef0123456789abcdef", Token: strings.Repeat("a", 32), Server: "http://127.0.0.1"}, &fakeCollector{})
		attempts := 0
		done := make(chan error, 1)
		go func() {
			done <- a.run(ctx, 2*time.Second, func(context.Context) (reportSender, error) {
				attempts++
				return &testSender{write: func(context.Context, report.Snapshot) error { return errInventoryRequired }}, nil
			})
		}()
		time.Sleep(time.Minute)
		synctest.Wait()
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if attempts < 5 || attempts > 7 {
			t.Fatalf("full inventory rejection did not back off: %d attempts in one minute", attempts)
		}
	})
}
