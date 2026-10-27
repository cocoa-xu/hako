package report

import "time"

const SchemaVersion = 1

type Section[T any] struct {
	Status     string    `json:"status"`
	ObservedAt time.Time `json:"observed_at"`
	Data       *T        `json:"data,omitempty"`
	Error      string    `json:"error,omitempty"`
}

func OK[T any](value T) Section[T] {
	return Section[T]{Status: "ok", ObservedAt: time.Now().UTC(), Data: &value}
}

func Unavailable[T any](status, reason string) Section[T] {
	return Section[T]{Status: status, ObservedAt: time.Now().UTC(), Error: reason}
}

type Snapshot struct {
	SchemaVersion int                  `json:"schema_version"`
	DeviceID      string               `json:"device_id"`
	ReportedAt    time.Time            `json:"reported_at"`
	Agent         Agent                `json:"agent"`
	Hardware      *Section[Hardware]   `json:"hardware,omitempty"`
	System        Section[System]      `json:"system"`
	Memory        Section[Memory]      `json:"memory"`
	Storage       Section[Storage]     `json:"storage"`
	Network       Section[[]Interface] `json:"network"`
	Security      Security             `json:"security"`
	LLDP          Section[[]Neighbor]  `json:"lldp"`
	SessionApps   Section[[]string]    `json:"session_apps"`
}

type Agent struct {
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`
	UID       int       `json:"uid"`
}

type Hardware struct {
	ModelName      string      `json:"model_name"`
	ModelID        string      `json:"model_id"`
	ModelNumber    string      `json:"model_number,omitempty"`
	Chip           string      `json:"chip"`
	Architecture   string      `json:"architecture"`
	PhysicalCores  int         `json:"physical_cores"`
	LogicalCores   int         `json:"logical_cores"`
	CoreGroups     []CoreGroup `json:"core_groups"`
	MemoryBytes    uint64      `json:"memory_bytes"`
	SerialNumber   string      `json:"serial_number,omitempty"`
	HardwareUUID   string      `json:"hardware_uuid,omitempty"`
	ProvisioningID string      `json:"provisioning_id,omitempty"`
	Firmware       string      `json:"firmware,omitempty"`
	OSLoader       string      `json:"os_loader,omitempty"`
	ActivationLock string      `json:"activation_lock,omitempty"`
}

type CoreGroup struct {
	Name          string `json:"name"`
	PhysicalCores int    `json:"physical_cores"`
	LogicalCores  int    `json:"logical_cores"`
}

type System struct {
	Hostname    string     `json:"hostname"`
	OSName      string     `json:"os_name"`
	OSVersion   string     `json:"os_version"`
	OSExtra     string     `json:"os_extra,omitempty"`
	OSBuild     string     `json:"os_build"`
	BootedAt    time.Time  `json:"booted_at"`
	Uptime      int64      `json:"uptime_seconds"`
	LoadAverage [3]float64 `json:"load_average"`
	ConsoleUser string     `json:"console_user,omitempty"`
}

type Memory struct {
	FreeBytes        uint64 `json:"free_bytes"`
	ActiveBytes      uint64 `json:"active_bytes"`
	InactiveBytes    uint64 `json:"inactive_bytes"`
	SpeculativeBytes uint64 `json:"speculative_bytes"`
	WiredBytes       uint64 `json:"wired_bytes"`
	CompressedBytes  uint64 `json:"compressed_bytes"`
	Pressure         string `json:"pressure"`
	SwapTotalBytes   uint64 `json:"swap_total_bytes"`
	SwapUsedBytes    uint64 `json:"swap_used_bytes"`
}

type Storage struct {
	Path           string `json:"path"`
	TotalBytes     uint64 `json:"total_bytes"`
	FreeBytes      uint64 `json:"free_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
}

type Interface struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac,omitempty"`
	Addresses []string `json:"addresses"`
	Up        bool     `json:"up"`
	Loopback  bool     `json:"loopback"`
}

type Security struct {
	RemoteLogin Section[bool]   `json:"remote_login"`
	FileVault   Section[bool]   `json:"filevault"`
	AutoLogin   Section[string] `json:"auto_login"`
}

type Neighbor struct {
	Interface string `json:"interface"`
	Name      string `json:"name"`
	Port      string `json:"port"`
}
