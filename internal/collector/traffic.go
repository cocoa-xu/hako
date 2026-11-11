package collector

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cocoa-xu/hako/internal/report"
)

type interfaceCounter struct {
	identity       string
	received, sent uint64
}

type trafficObservation struct {
	at       time.Time
	counters map[string]interfaceCounter
}

var ethernetName = regexp.MustCompile(`^en[0-9]+$`)

func parseTraffic(output []byte) (map[string]interfaceCounter, error) {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	header := strings.Fields(lines[0])
	columns := map[string]int{}
	for i, name := range header {
		columns[name] = i
	}
	for _, name := range []string{"Name", "Network", "Ibytes", "Obytes"} {
		if _, found := columns[name]; !found {
			return nil, fmt.Errorf("netstat is missing the %s column", name)
		}
	}
	counters := map[string]interfaceCounter{}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 || !strings.HasPrefix(fields[2], "<Link#") {
			continue
		}
		name := strings.TrimSuffix(fields[0], "*")
		if !ethernetName.MatchString(name) {
			continue
		}
		if len(fields) != len(header) || !strings.Contains(fields[3], ":") {
			return nil, fmt.Errorf("incomplete netstat counters for %s", name)
		}
		received, err := strconv.ParseUint(fields[columns["Ibytes"]], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid receive counter for %s", name)
		}
		sent, err := strconv.ParseUint(fields[columns["Obytes"]], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid send counter for %s", name)
		}
		if _, duplicate := counters[name]; duplicate {
			return nil, fmt.Errorf("duplicate link counters for %s", name)
		}
		counters[name] = interfaceCounter{identity: fields[2] + fields[3], received: received, sent: sent}
	}
	if len(counters) == 0 {
		return nil, fmt.Errorf("no Ethernet or Wi-Fi counters available")
	}
	return counters, nil
}

func trafficBetween(previous *trafficObservation, current trafficObservation) report.Traffic {
	traffic := report.Traffic{Interfaces: make([]string, 0, len(current.counters))}
	for name, counter := range current.counters {
		traffic.ReceivedBytes += counter.received
		traffic.SentBytes += counter.sent
		traffic.Interfaces = append(traffic.Interfaces, name)
	}
	sort.Strings(traffic.Interfaces)
	if previous == nil || len(previous.counters) != len(current.counters) {
		return traffic
	}
	seconds := current.at.Sub(previous.at).Seconds()
	if seconds <= 0 {
		return traffic
	}
	var received, sent uint64
	for name, counter := range current.counters {
		old, found := previous.counters[name]
		if !found || counter.identity != old.identity || counter.received < old.received || counter.sent < old.sent {
			return traffic
		}
		received += counter.received - old.received
		sent += counter.sent - old.sent
	}
	traffic.Rate = &report.TrafficRate{ReceiveBytesPerSecond: float64(received) / seconds, SendBytesPerSecond: float64(sent) / seconds, WindowSeconds: seconds}
	return traffic
}
