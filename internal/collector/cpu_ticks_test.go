package collector

import (
	"math"
	"testing"
	"time"
)

func TestCPUTicksMeasureBusyCoresIncludingCounterWrap(t *testing.T) {
	start := time.Now()
	for _, test := range []struct {
		name          string
		before, after [4]uint32
		want          float64
	}{
		{"one core", [4]uint32{}, [4]uint32{50, 25, 1100, 25}, 100},
		{"fully busy", [4]uint32{}, [4]uint32{900, 300, 0, 0}, 1200},
		{"idle", [4]uint32{}, [4]uint32{0, 0, 1200, 0}, 0},
		{"wrapped ticks", [4]uint32{math.MaxUint32 - 49, 0, 0, 0}, [4]uint32{50, 0, 1100, 0}, 100},
	} {
		t.Run(test.name, func(t *testing.T) {
			cpu, err := cpuBetween(&cpuObservation{at: start, ticks: test.before}, cpuObservation{at: start.Add(2 * time.Second), ticks: test.after}, 12)
			if err != nil || math.Abs(cpu.UsagePercent-test.want) > .001 || cpu.WindowSeconds != 2 {
				t.Fatalf("got %+v, %v", cpu, err)
			}
		})
	}
	if _, err := cpuBetween(nil, cpuObservation{}, 12); err == nil {
		t.Fatal("invented initial CPU value")
	}
	if _, err := cpuBetween(&cpuObservation{at: start}, cpuObservation{at: start.Add(time.Second)}, 12); err == nil {
		t.Fatal("accepted stationary ticks")
	}
}
