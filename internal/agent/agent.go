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

	"hako/internal/identity"
	"hako/internal/report"
)

type Config struct {
	DeviceID string `json:"device_id"`
	Server   string `json:"server"`
	Token    string `json:"token"`
	Pending  bool   `json:"pending,omitempty"`
}

// ErrUnauthorized means the worker must be enrolled again by an administrator.
var ErrUnauthorized = errors.New("worker credential was rejected or revoked")

func (c Config) Validate() error {
	u, err := url.Parse(c.Server)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("server must be an HTTP(S) origin without credentials, a path, or a query")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := ip != nil && ip.IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return fmt.Errorf("server requires HTTPS, except for a loopback IP address")
	}
	if !identity.Valid(identity.Normalize(c.DeviceID)) || len(c.Token) < 32 {
		return fmt.Errorf("invalid agent credentials")
	}
	return nil
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
	for {
		select {
		case <-ctx.Done():
			return nil
		case snapshot := <-samples:
			if err := a.send(ctx, snapshot); errors.Is(err, ErrUnauthorized) {
				return err
			} else if err != nil && ctx.Err() == nil {
				slog.Warn("report failed; retrying with the latest sample", "error", err)
			}
		}
	}
}
