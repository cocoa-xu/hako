package collector

import (
	"fmt"
	"math"
	"testing"
)

func TestCPUUsesIntervalSampleAndOneCoreScale(t *testing.T) {
	for _, test := range []struct {
		name, second string
		cores        int
		want         float64
	}{
		{"one busy core", "25.00% user, 0.00% sys, 75.00% idle", 4, 100},
		{"multiple busy cores", "20.00% user, 5.00% sys, 75.00% idle", 12, 300},
		{"fully busy", "90.00% user, 10.00% sys, 0.00% idle", 12, 1200},
		{"idle", "0.00% user, 0.00% sys, 100.00% idle", 8, 0},
		{"rounded counters", "4.36% user, 9.94% sys, 85.69% idle", 12, 171.6},
		{"unpadded hundredths", "4.7% user, 9.3% sys, 86.90% idle", 12, 157.2},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := fmt.Sprintf("CPU usage: 91.00%% user, 9.00%% sys, 0.00%% idle\nOther statistics\nCPU usage: %s \n", test.second)
			cpu, err := parseCPU([]byte(output), test.cores)
			if err != nil || math.Abs(cpu.UsagePercent-test.want) > 0.001 || cpu.LogicalCores != test.cores {
				t.Fatalf("got %+v, %v; want %.1f%% on %d cores", cpu, err, test.want, test.cores)
			}
		})
	}
}

func TestCPURejectsIncompleteAndInvalidSamples(t *testing.T) {
	first := "CPU usage: 10.00% user, 5.00% sys, 85.00% idle\n"
	for _, input := range []string{
		"", first, first + "CPU usage: unavailable\n",
		first + "CPU usage: 80% user, 80% sys, 0% idle\n",
		first + "CPU usage: 101% user, 0% sys, 0% idle\n",
		first + "CPU usage: NaN% user, 0% sys, 100% idle\n",
		first + "CPU usage: -1% user, 1% sys, 100% idle\n",
	} {
		if _, err := parseCPU([]byte(input), 12); err == nil {
			t.Errorf("accepted invalid samples: %q", input)
		}
	}
	if _, err := parseCPU([]byte(first+first), 0); err == nil {
		t.Fatal("accepted zero CPU cores")
	}
}
