package collector

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/cocoa-xu/hako/internal/report"
)

var cpuPattern = regexp.MustCompile(`(?m)^CPU usage:\s*([0-9]+(?:\.[0-9]+)?)% user,\s*([0-9]+(?:\.[0-9]+)?)% sys,\s*([0-9]+(?:\.[0-9]+)?)% idle\s*$`)

func parseCPU(output []byte, logicalCores int) (report.CPU, error) {
	if logicalCores <= 0 {
		return report.CPU{}, fmt.Errorf("invalid logical CPU count")
	}
	samples := cpuPattern.FindAllSubmatch(output, -1)
	if len(samples) != 2 {
		return report.CPU{}, fmt.Errorf("top did not return two CPU samples")
	}
	var values [3]float64
	for i := range values {
		value, err := strconv.ParseFloat(string(samples[1][i+1]), 64)
		if err != nil || value < 0 || value > 100 {
			return report.CPU{}, fmt.Errorf("invalid CPU percentage")
		}
		values[i] = value
	}
	if math.Abs(values[0]+values[1]+values[2]-100) > 0.1 {
		// Some versions of top print hundredths without a leading zero.
		for i := range values {
			parts := strings.SplitN(string(samples[1][i+1]), ".", 2)
			if len(parts) == 2 && len(parts[1]) == 1 {
				values[i], _ = strconv.ParseFloat(parts[0]+".0"+parts[1], 64)
			}
		}
	}
	if math.Abs(values[0]+values[1]+values[2]-100) > 0.1 {
		return report.CPU{}, fmt.Errorf("CPU percentages do not total 100")
	}
	return report.CPU{
		UsagePercent: min(100, values[0]+values[1]) * float64(logicalCores),
		LogicalCores: logicalCores,
	}, nil
}
