package collector

import (
	"strings"
	"testing"
	"time"
)

const netstatSample = `Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll
lo0 16384 <Link#1> 100 0 999999 100 0 999999 0
en0 1500 <Link#7> 02:00:00:00:00:01 100 0 5000000000 50 0 8000000000 0
en0 1500 192.0.2 192.0.2.1 100 - 5000000000 50 - 8000000000 -
en0 1500 fe80:: fe80::1 100 - 5000000000 50 - 8000000000 -
en1* 1500 <Link#8> 02:00:00:00:00:02 20 0 2000 10 0 1000 0
utun0 1500 <Link#9> 200 0 999999 200 0 999999 0
awdl0 1500 <Link#10> 02:00:00:00:00:03 200 0 999999 200 0 999999 0
bridge0 1500 <Link#11> 02:00:00:00:00:04 200 0 999999 200 0 999999 0
`

func TestTrafficUses64BitLinkCountersWithoutDuplicatingAddresses(t *testing.T) {
	counters, err := parseTraffic([]byte(netstatSample))
	if err != nil {
		t.Fatal(err)
	}
	traffic := trafficBetween(nil, trafficObservation{counters: counters})
	if len(traffic.Interfaces) != 2 || traffic.ReceivedBytes != 5000002000 || traffic.SentBytes != 8000001000 || traffic.Rate != nil {
		t.Fatalf("incorrect totals or invented initial rate: %+v", traffic)
	}
	for _, bad := range []string{"", strings.ReplaceAll(netstatSample, "Ibytes", "unknown"), strings.ReplaceAll(netstatSample, "5000000000", "-"), netstatSample + strings.Split(netstatSample, "\n")[2]} {
		if _, err := parseTraffic([]byte(bad)); err == nil {
			t.Fatal("accepted invalid counters")
		}
	}
}

func TestTrafficUsesActualElapsedTimeAndResetsRateAfterChanges(t *testing.T) {
	start := time.Now()
	previous := trafficObservation{at: start, counters: map[string]interfaceCounter{"en0": {identity: "link7", received: 10000, sent: 5000}}}
	current := trafficObservation{at: start.Add(2500 * time.Millisecond), counters: map[string]interfaceCounter{"en0": {identity: "link7", received: 15000, sent: 6000}}}
	traffic := trafficBetween(&previous, current)
	if traffic.Rate == nil || traffic.Rate.ReceiveBytesPerSecond != 2000 || traffic.Rate.SendBytesPerSecond != 400 || traffic.Rate.WindowSeconds != 2.5 {
		t.Fatalf("incorrect interval rate: %+v", traffic.Rate)
	}
	for _, counters := range []map[string]interfaceCounter{
		{"en0": {identity: "link7", received: 1, sent: 1}},
		{"en0": {identity: "link8", received: 15000, sent: 6000}},
		{"en1": {identity: "link7", received: 15000, sent: 6000}},
		{},
		{"en0": previous.counters["en0"], "en1": {}},
	} {
		current.counters = counters
		if trafficBetween(&previous, current).Rate != nil {
			t.Fatal("invented rate across reset or interface change")
		}
	}
	previous.at = current.at
	current.counters = previous.counters
	if trafficBetween(&previous, current).Rate != nil {
		t.Fatal("accepted zero sampling duration")
	}
}
