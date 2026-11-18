package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cocoa-xu/hako/internal/identity"
	"github.com/cocoa-xu/hako/internal/report"
)

type Config struct {
	DeviceID         string              `json:"device_id"`
	Server           string              `json:"server"`
	Token            string              `json:"token"`
	Pending          bool                `json:"pending,omitempty"`
	CloudflareAccess *AccessServiceToken `json:"cloudflare_access,omitempty"`
	AllowHTTP        bool                `json:"allow_http,omitempty"`
}

// ErrUnauthorized means Hako or its access proxy rejected worker authentication.
var ErrUnauthorized = errors.New("worker authentication was rejected; check Hako credentials and Cloudflare Access")

func (c Config) Validate() error {
	u, err := url.Parse(c.Server)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("server must be an HTTP(S) origin without credentials, a path, or a query")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := ip != nil && ip.IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && (loopback || c.AllowHTTP)) {
		return fmt.Errorf("server requires HTTPS; use --allow-http only for a trusted private network")
	}
	if !identity.Valid(identity.Normalize(c.DeviceID)) || len(c.Token) < 32 {
		return fmt.Errorf("invalid agent credentials")
	}
	return c.CloudflareAccess.validate()
}

type Collector interface {
	Inventory(context.Context) report.Section[report.Hardware]
	Sample(context.Context, string) report.Snapshot
}

type Agent struct {
	config             Config
	collector          Collector
	client             *http.Client
	inventory          *report.Section[report.Hardware]
	deliveredInventory *report.Section[report.Hardware]
}

func New(config Config, collector Collector) (*Agent, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if config.Pending {
		return nil, fmt.Errorf("registration has not completed; retry join with the same configuration")
	}
	return &Agent{config: config, collector: collector, client: &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (a *Agent) collect(ctx context.Context) report.Snapshot {
	if a.inventory == nil || a.inventory.Status != "ok" {
		inventory := a.collector.Inventory(ctx)
		a.inventory = &inventory
	}
	snapshot := a.collector.Sample(ctx, a.config.DeviceID)
	snapshot.Hardware = a.inventory
	return snapshot
}

func (a *Agent) Step(ctx context.Context) error {
	return a.send(ctx, a.collect(ctx))
}

func (a *Agent) send(ctx context.Context, snapshot report.Snapshot) error {
	inventory := snapshot.Hardware
	if inventory == a.deliveredInventory {
		snapshot.Hardware = nil
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.config.Server, "/")+"/api/reports", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.config.Token)
	a.config.CloudflareAccess.Apply(req)
	req.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode == http.StatusConflict {
		a.deliveredInventory = nil
		return fmt.Errorf("receiver requested the cached startup inventory")
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return ErrUnauthorized
	}
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("receiver returned HTTP %d", response.StatusCode)
	}
	a.deliveredInventory = inventory
	return nil
}

func (a *Agent) Run(ctx context.Context, interval time.Duration) error {
	return a.run(ctx, interval, a.connect)
}

func (a *Agent) run(ctx context.Context, interval time.Duration, connect func(context.Context) (reportSender, error)) error {
	if interval < time.Second {
		return fmt.Errorf("report interval must be at least one second")
	}
	ctx, cancel := context.WithCancel(ctx)
	samples := make(chan report.Snapshot, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for ctx.Err() == nil {
			snapshot := a.collect(ctx)
			select {
			case samples <- snapshot:
			default:
				// Keep only the latest sample while a receiver is slow or offline.
				select {
				case <-samples:
				default:
				}
				select {
				case samples <- snapshot:
				case <-ctx.Done():
					return
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { cancel(); <-done }()
	var stream reportSender
	defer func() {
		if stream != nil {
			stream.close()
		}
	}()
	failures := 0
	var connectedAt time.Time
	var pending *report.Snapshot
	reconnect := func(err error) {
		delay := retryDelay(failures)
		var upstream *retryAfterError
		if errors.As(err, &upstream) {
			delay = max(delay, upstream.delay)
		}
		failures = min(failures+1, 5)
		if ctx.Err() == nil {
			if errors.Is(err, errStreamRenewal) {
				slog.Debug("renewing report stream", "delay", delay)
			} else {
				slog.Warn("report stream disconnected; reconnecting", "delay", delay, "error", err)
			}
			waitToReconnect(ctx, delay)
		}
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		if stream == nil {
			var err error
			stream, err = connect(ctx)
			if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrStreamingUnsupported) {
				return err
			}
			if err != nil {
				reconnect(err)
				continue
			}
			connectedAt = time.Now()
			a.deliveredInventory = nil
		}
		if pending == nil {
			select {
			case <-ctx.Done():
				return nil
			case snapshot := <-samples:
				pending = &snapshot
			}
		} else {
			select {
			case snapshot := <-samples:
				pending = &snapshot
			default:
			}
		}
		snapshot := *pending
		inventory := snapshot.Hardware
		if inventory == a.deliveredInventory {
			snapshot.Hardware = nil
		}
		err := stream.send(ctx, snapshot)
		if errors.Is(err, ErrUnauthorized) {
			return err
		}
		if errors.Is(err, errInventoryRequired) {
			a.deliveredInventory = nil
			if snapshot.Hardware == nil {
				continue
			}
			err = fmt.Errorf("receiver rejected startup inventory")
		}
		if err != nil {
			stream.close()
			stream = nil
			if time.Since(connectedAt) >= time.Minute {
				failures = 0
			}
			reconnect(err)
			continue
		}
		a.deliveredInventory = inventory
		pending = nil
	}
}
