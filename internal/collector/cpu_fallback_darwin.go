//go:build !cgo

package collector

import (
	"context"
	"runtime"

	"hako/internal/report"
)

func (c *Collector) cpu(ctx context.Context) (report.CPU, error) {
	data, err := c.run(ctx, "/usr/bin/top", "-l", "2", "-s", "1", "-n", "0", "-F", "-R")
	if err != nil {
		return report.CPU{}, err
	}
	cpu, err := parseCPU(data, runtime.NumCPU())
	cpu.WindowSeconds = 1
	return cpu, err
}
