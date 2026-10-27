package collector

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"hako/internal/report"
)

func parseHardware(data []byte) (report.Hardware, error) {
	var root struct {
		Hardware []map[string]json.RawMessage `json:"SPHardwareDataType"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return report.Hardware{}, err
	}
	if len(root.Hardware) != 1 {
		return report.Hardware{}, fmt.Errorf("expected one hardware record, got %d", len(root.Hardware))
	}
	fields := root.Hardware[0]
	str := func(key string) string {
		var value string
		_ = json.Unmarshal(fields[key], &value)
		return value
	}
	h := report.Hardware{
		ModelName: str("machine_name"), ModelID: str("machine_model"), ModelNumber: str("model_number"),
		Chip: str("chip_type"), SerialNumber: str("serial_number"), HardwareUUID: str("platform_UUID"),
		ProvisioningID: str("provisioning_UDID"), Firmware: str("boot_rom_version"), OSLoader: str("os_loader_version"),
	}
	if h.Chip == "" {
		h.Chip = str("cpu_type")
	}
	switch str("activation_lock_status") {
	case "activation_lock_enabled":
		h.ActivationLock = "enabled"
	case "activation_lock_disabled":
		h.ActivationLock = "disabled"
	default:
		h.ActivationLock = "unknown"
	}
	if h.ModelID == "" || h.ModelName == "" {
		return h, fmt.Errorf("hardware report is missing model identity")
	}
	return h, nil
}

func keyValues(data []byte) map[string]string {
	values := make(map[string]string)
	for line := range strings.SplitSeq(string(data), "\n") {
		if key, value, ok := strings.Cut(line, ":"); ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return values
}

var pageSizePattern = regexp.MustCompile(`page size of (\d+) bytes`)

func parseVM(data []byte) (report.Memory, error) {
	var m report.Memory
	match := pageSizePattern.FindSubmatch(data)
	if len(match) != 2 {
		return m, fmt.Errorf("vm_stat did not report its page size")
	}
	pageSize, err := strconv.ParseUint(string(match[1]), 10, 64)
	if err != nil || pageSize == 0 {
		return m, fmt.Errorf("invalid vm_stat page size")
	}
	values := keyValues(data)
	for key, destination := range map[string]*uint64{
		"Pages free": &m.FreeBytes, "Pages active": &m.ActiveBytes,
		"Pages inactive": &m.InactiveBytes, "Pages speculative": &m.SpeculativeBytes,
		"Pages wired down": &m.WiredBytes, "Pages occupied by compressor": &m.CompressedBytes,
	} {
		pages, err := strconv.ParseUint(strings.TrimSuffix(values[key], "."), 10, 64)
		if err != nil || pages > ^uint64(0)/pageSize {
			return m, fmt.Errorf("invalid vm_stat field %q", key)
		}
		*destination = pages * pageSize
	}
	return m, nil
}

var swapPattern = regexp.MustCompile(`\b(total|used)\s*=\s*([0-9]+(?:\.[0-9]+)?)([KMGT])`)

func parseSwap(text string) (total, used uint64, err error) {
	values := make(map[string]uint64)
	for _, match := range swapPattern.FindAllStringSubmatch(text, -1) {
		number, e := strconv.ParseFloat(match[2], 64)
		if e != nil {
			return 0, 0, e
		}
		multiplier := map[string]float64{"K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}[match[3]]
		values[match[1]] = uint64(number * multiplier)
	}
	if len(values) != 2 || values["used"] > values["total"] {
		return 0, 0, fmt.Errorf("invalid swap usage response")
	}
	return values["total"], values["used"], nil
}
