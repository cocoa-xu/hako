package collector

import (
	"fmt"
	"time"

	"github.com/cocoa-xu/hako/internal/report"
)

type cpuObservation struct {
	at    time.Time
	ticks [4]uint32
}

func cpuBetween(previous *cpuObservation, current cpuObservation, cores int) (report.CPU, error) {
	if previous == nil {
		return report.CPU{}, fmt.Errorf("waiting for the next CPU sample")
	}
	seconds := current.at.Sub(previous.at).Seconds()
	if cores <= 0 || seconds <= 0 {
		return report.CPU{}, fmt.Errorf("invalid CPU sampling window")
	}
	var total, idle uint64
	for i, ticks := range current.ticks {
		// Darwin exposes 32-bit ticks; unsigned subtraction handles wraparound.
		delta := uint64(ticks - previous.ticks[i])
		total += delta
		if i == 2 {
			idle = delta
		}
	}
	if total == 0 {
		return report.CPU{}, fmt.Errorf("CPU counters did not advance")
	}
	return report.CPU{UsagePercent: float64(total-idle) / float64(total) * float64(cores) * 100, LogicalCores: cores, WindowSeconds: seconds}, nil
}
