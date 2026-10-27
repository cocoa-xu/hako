package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hako/internal/report"
)

type Config struct {
	DeviceID string `json:"device_id"`
	Server   string `json:"server"`
	Token    string `json:"token"`
}

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
	if len(c.DeviceID) != 32 || strings.Trim(c.DeviceID, "0123456789abcdef") != "" || len(c.Token) < 32 {
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
	inventoryDelivered bool
}

func New(config Config, collector Collector) (*Agent, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Agent{config: config, collector: collector, client: &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (a *Agent) Step(ctx context.Context) error {
	if a.inventory == nil || a.inventory.Status != "ok" {
		inventory := a.collector.Inventory(ctx)
		a.inventory = &inventory
		a.inventoryDelivered = false
	}
	snapshot := a.collector.Sample(ctx, a.config.DeviceID)
	if !a.inventoryDelivered {
		snapshot.Hardware = a.inventory
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
		a.inventoryDelivered = false
		return fmt.Errorf("receiver requested the cached startup inventory")
	}
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("receiver returned HTTP %d", response.StatusCode)
	}
	a.inventoryDelivered = true
	return nil
}

func (a *Agent) Run(ctx context.Context, interval time.Duration) error {
	if interval < time.Second {
		return fmt.Errorf("report interval must be at least one second")
	}
	for {
		if err := a.Step(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("report failed; retrying at the next interval", "error", err)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
