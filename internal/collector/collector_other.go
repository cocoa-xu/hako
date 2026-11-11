//go:build !darwin

package collector

import (
	"context"
	"os"
	"time"

	"github.com/cocoa-xu/hako/internal/report"
)

type Collector struct{ agent report.Agent }

func New(version string) *Collector {
	return &Collector{agent: report.Agent{Version: version, StartedAt: time.Now().UTC(), UID: os.Geteuid()}}
}

func (c *Collector) Inventory(context.Context) report.Section[report.Hardware] {
	return report.Unavailable[report.Hardware]("unsupported", "The collector requires macOS.")
}

func (c *Collector) Sample(_ context.Context, deviceID string) report.Snapshot {
	return report.Snapshot{SchemaVersion: report.SchemaVersion, DeviceID: deviceID, Agent: c.agent, ReportedAt: time.Now().UTC()}
}
