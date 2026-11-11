package collector

import (
	"context"
	"time"

	"github.com/cocoa-xu/hako/internal/report"
)

func (c *Collector) traffic(ctx context.Context) (report.Traffic, error) {
	output, err := c.run(ctx, "/usr/sbin/netstat", "-ibn")
	if err != nil {
		c.trafficPrevious = nil
		return report.Traffic{}, err
	}
	counters, err := parseTraffic(output)
	if err != nil {
		c.trafficPrevious = nil
		return report.Traffic{}, err
	}
	current := trafficObservation{at: time.Now(), counters: counters}
	traffic := trafficBetween(c.trafficPrevious, current)
	c.trafficPrevious = &current
	return traffic, nil
}
