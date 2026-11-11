package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/user"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cocoa-xu/hako/internal/report"
)

type Collector struct {
	run             runner
	agent           report.Agent
	cpuPrevious     *cpuObservation
	trafficPrevious *trafficObservation
	securityCache   report.Security
	securityAt      time.Time
}

func New(version string) *Collector {
	return &Collector{run: command, agent: report.Agent{Version: version, StartedAt: time.Now().UTC(), UID: os.Geteuid()}}
}

func section[T any](value T, err error) report.Section[T] {
	if err != nil {
		return report.Unavailable[T]("error", err.Error())
	}
	return report.OK(value)
}

func (c *Collector) Inventory(ctx context.Context) report.Section[report.Hardware] {
	data, err := c.run(ctx, "/usr/sbin/system_profiler", "SPHardwareDataType", "-json", "-timeout", "20")
	if err != nil {
		return section(report.Hardware{}, err)
	}
	hardware, err := parseHardware(data)
	if err != nil {
		return section(hardware, err)
	}
	hardware.Architecture = runtime.GOARCH
	data, err = c.run(ctx, "/usr/sbin/sysctl", "hw.memsize", "hw.physicalcpu", "hw.logicalcpu")
	if err != nil {
		return section(hardware, err)
	}
	values := keyValues(data)
	if hardware.MemoryBytes, err = strconv.ParseUint(values["hw.memsize"], 10, 64); err != nil {
		return section(hardware, fmt.Errorf("invalid installed memory: %w", err))
	}
	if hardware.PhysicalCores, err = strconv.Atoi(values["hw.physicalcpu"]); err != nil {
		return section(hardware, fmt.Errorf("invalid physical core count: %w", err))
	}
	if hardware.LogicalCores, err = strconv.Atoi(values["hw.logicalcpu"]); err != nil {
		return section(hardware, fmt.Errorf("invalid logical core count: %w", err))
	}
	hardware.CoreGroups = []report.CoreGroup{}
	data, err = c.run(ctx, "/usr/sbin/sysctl", "-n", "hw.nperflevels")
	if err == nil {
		levels, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || levels < 0 || levels > 32 {
			return section(hardware, fmt.Errorf("invalid CPU performance level count"))
		}
		for i := 0; i < levels; i++ {
			prefix := fmt.Sprintf("hw.perflevel%d.", i)
			data, err = c.run(ctx, "/usr/sbin/sysctl", prefix+"name", prefix+"physicalcpu", prefix+"logicalcpu")
			if err != nil {
				return section(hardware, err)
			}
			values := keyValues(data)
			physical, err := strconv.Atoi(values[prefix+"physicalcpu"])
			if err != nil {
				return section(hardware, fmt.Errorf("invalid physical cores for level %d", i))
			}
			logical, err := strconv.Atoi(values[prefix+"logicalcpu"])
			if err != nil {
				return section(hardware, fmt.Errorf("invalid logical cores for level %d", i))
			}
			name := values[prefix+"name"]
			if name == "" {
				name = fmt.Sprintf("Level %d", i)
			}
			hardware.CoreGroups = append(hardware.CoreGroups, report.CoreGroup{Name: name, PhysicalCores: physical, LogicalCores: logical})
		}
	}
	return section(hardware, nil)
}

func (c *Collector) Sample(ctx context.Context, deviceID string) report.Snapshot {
	snapshot := report.Snapshot{
		SchemaVersion: report.SchemaVersion, DeviceID: deviceID, Agent: c.agent,
		LLDP:        report.Unavailable[[]report.Neighbor]("unsupported", "LLDP collection is not implemented yet."),
		SessionApps: report.Unavailable[[]string]("unsupported", "A user-session collector is not implemented yet."),
	}
	snapshot.System = section(c.system(ctx))
	snapshot.CPU = section(c.cpu(ctx))
	snapshot.Memory = section(c.memory(ctx))
	snapshot.Storage = section(storage())
	snapshot.Network = section(interfaces())
	snapshot.Traffic = section(c.traffic(ctx))
	if c.securityAt.IsZero() || time.Since(c.securityAt) >= 30*time.Second {
		c.securityCache = c.security(ctx)
		c.securityAt = time.Now()
	}
	snapshot.Security = c.securityCache
	snapshot.ReportedAt = time.Now().UTC()
	return snapshot
}

var bootPattern = regexp.MustCompile(`\bsec\s*=\s*(\d+)`)

func (c *Collector) system(ctx context.Context) (report.System, error) {
	var system report.System
	hostname, err := os.Hostname()
	if err != nil {
		return system, err
	}
	system.Hostname = hostname
	data, err := c.run(ctx, "/usr/bin/sw_vers")
	if err != nil {
		return system, err
	}
	values := keyValues(data)
	system.OSName, system.OSVersion = values["ProductName"], values["ProductVersion"]
	system.OSExtra, system.OSBuild = values["ProductVersionExtra"], values["BuildVersion"]
	if system.OSVersion == "" || system.OSBuild == "" {
		return system, fmt.Errorf("sw_vers returned an incomplete OS version")
	}
	data, err = c.run(ctx, "/usr/sbin/sysctl", "kern.boottime", "vm.loadavg")
	if err != nil {
		return system, err
	}
	values = keyValues(data)
	boot := bootPattern.FindStringSubmatch(values["kern.boottime"])
	if len(boot) != 2 {
		return system, fmt.Errorf("invalid boot time")
	}
	seconds, err := strconv.ParseInt(boot[1], 10, 64)
	if err != nil {
		return system, err
	}
	system.BootedAt = time.Unix(seconds, 0).UTC()
	system.Uptime = max(0, int64(time.Since(system.BootedAt).Seconds()))
	load := strings.Fields(strings.Trim(values["vm.loadavg"], "{} "))
	if len(load) != 3 {
		return system, fmt.Errorf("invalid load average")
	}
	for i := range system.LoadAverage {
		if system.LoadAverage[i], err = strconv.ParseFloat(load[i], 64); err != nil {
			return system, err
		}
	}
	var stat syscall.Stat_t
	if syscall.Stat("/dev/console", &stat) == nil && stat.Uid != 0 {
		if consoleUser, err := user.LookupId(strconv.FormatUint(uint64(stat.Uid), 10)); err == nil {
			system.ConsoleUser = consoleUser.Username
		}
	}
	return system, nil
}

func (c *Collector) memory(ctx context.Context) (report.Memory, error) {
	data, err := c.run(ctx, "/usr/bin/vm_stat")
	if err != nil {
		return report.Memory{}, err
	}
	memory, err := parseVM(data)
	if err != nil {
		return memory, err
	}
	data, err = c.run(ctx, "/usr/sbin/sysctl", "-n", "vm.swapusage")
	if err != nil {
		return memory, err
	}
	memory.SwapTotalBytes, memory.SwapUsedBytes, err = parseSwap(string(data))
	if err != nil {
		return memory, err
	}
	memory.Pressure = "unknown"
	data, err = c.run(ctx, "/usr/sbin/sysctl", "-n", "kern.memorystatus_vm_pressure_level")
	if err == nil {
		switch strings.TrimSpace(string(data)) {
		case "1":
			memory.Pressure = "normal"
		case "2":
			memory.Pressure = "warning"
		case "4":
			memory.Pressure = "critical"
		}
	}
	return memory, nil
}

func storage() (report.Storage, error) {
	path := "/System/Volumes/Data"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		path = "/"
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return report.Storage{}, err
	}
	block := uint64(stat.Bsize)
	return report.Storage{Path: path, TotalBytes: stat.Blocks * block, FreeBytes: stat.Bfree * block, AvailableBytes: stat.Bavail * block}, nil
}

func interfaces() ([]report.Interface, error) {
	devices, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	result := make([]report.Interface, 0, len(devices))
	for _, device := range devices {
		addresses, err := device.Addrs()
		if err != nil {
			return nil, fmt.Errorf("interface %s: %w", device.Name, err)
		}
		item := report.Interface{Name: device.Name, MAC: device.HardwareAddr.String(), Up: device.Flags&net.FlagUp != 0, Loopback: device.Flags&net.FlagLoopback != 0, Addresses: []string{}}
		for _, address := range addresses {
			item.Addresses = append(item.Addresses, address.String())
		}
		sort.Strings(item.Addresses)
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (c *Collector) security(ctx context.Context) report.Security {
	result := report.Security{
		RemoteLogin: report.Unavailable[bool]("permission_denied", "Remote Login status requires root."),
	}
	if c.agent.UID == 0 {
		data, err := c.run(ctx, "/usr/sbin/systemsetup", "-getremotelogin")
		result.RemoteLogin = parseToggle(data, err, "Remote Login: On", "Remote Login: Off")
	}
	data, err := c.run(ctx, "/usr/bin/fdesetup", "status")
	result.FileVault = parseToggle(data, err, "FileVault is On.", "FileVault is Off.")
	path := "/Library/Preferences/com.apple.loginwindow.plist"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		result.AutoLogin = report.OK("")
	} else {
		data, err := c.run(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", path)
		if err != nil {
			result.AutoLogin = report.Unavailable[string]("error", err.Error())
		} else {
			var prefs struct {
				User string `json:"autoLoginUser"`
			}
			err := json.Unmarshal(data, &prefs)
			result.AutoLogin = section(prefs.User, err)
		}
	}
	return result
}

func parseToggle(data []byte, err error, on, off string) report.Section[bool] {
	if err != nil {
		return report.Unavailable[bool]("error", err.Error())
	}
	text := string(data)
	if strings.Contains(text, on) {
		return report.OK(true)
	}
	if strings.Contains(text, off) {
		return report.OK(false)
	}
	return report.Unavailable[bool]("error", "The system returned an unrecognized status.")
}
