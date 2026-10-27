package collector

import (
	"fmt"
	"testing"
)

func TestHardwareRejectsMissingIdentity(t *testing.T) {
	for _, input := range []string{`{}`, `{"SPHardwareDataType":[]}`, `{"SPHardwareDataType":[{"serial_number":"SAMPLE"}]}`, `not json`} {
		if _, err := parseHardware([]byte(input)); err == nil {
			t.Errorf("accepted invalid hardware: %s", input)
		}
	}
}

func TestHardwareUnknownActivationLockIsNotDisabled(t *testing.T) {
	hardware, err := parseHardware([]byte(`{"SPHardwareDataType":[{"machine_name":"Mac mini","machine_model":"MacSample,1","chip_type":"Apple sample","activation_lock_status":"future_status"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if hardware.ActivationLock != "unknown" {
		t.Fatalf("got %q", hardware.ActivationLock)
	}
}

func TestVMUsesReportedPageSize(t *testing.T) {
	for _, size := range []uint64{4096, 16384} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			data := fmt.Sprintf("Mach Virtual Memory Statistics: (page size of %d bytes)\nPages free: 2.\nPages active: 3.\nPages inactive: 4.\nPages speculative: 5.\nPages wired down: 6.\nPages occupied by compressor: 7.\n", size)
			memory, err := parseVM([]byte(data))
			if err != nil {
				t.Fatal(err)
			}
			if memory.FreeBytes != 2*size || memory.CompressedBytes != 7*size {
				t.Fatalf("wrong byte counts: %+v", memory)
			}
		})
	}
	if _, err := parseVM([]byte("Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free: 1.\n")); err == nil {
		t.Fatal("missing fields must not become zero")
	}
}

func TestSwapValues(t *testing.T) {
	for _, test := range []struct {
		input       string
		total, used uint64
	}{
		{"total = 1024.00M  used = 0.25M  free = 1023.75M (encrypted)", 1 << 30, 1 << 18},
		{"total = 0.00M used = 0.00M free = 0.00M", 0, 0},
	} {
		total, used, err := parseSwap(test.input)
		if err != nil || total != test.total || used != test.used {
			t.Fatalf("parseSwap(%q) = %d, %d, %v", test.input, total, used, err)
		}
	}
	for _, input := range []string{"total = 1024.00M", "total = 0M used = 1M", "unavailable"} {
		if _, _, err := parseSwap(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}
