package collector

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestInventoryPreservesAllCoreGroups(t *testing.T) {
	c := New("test")
	c.run = func(_ context.Context, path string, args ...string) ([]byte, error) {
		if path == "/usr/sbin/system_profiler" {
			return []byte(`{"SPHardwareDataType":[{"machine_name":"Mac mini","machine_model":"MacSample,1","chip_type":"Apple sample"}]}`), nil
		}
		if args[0] == "hw.memsize" {
			return []byte("hw.memsize: 25769803776\nhw.physicalcpu: 12\nhw.logicalcpu: 12\n"), nil
		}
		if args[0] == "-n" {
			return []byte("3\n"), nil
		}
		var level int
		if _, err := fmt.Sscanf(args[0], "hw.perflevel%d.name", &level); err != nil {
			return nil, err
		}
		name := []string{"Super", "Performance", "Efficiency"}[level]
		cores := []int{2, 4, 6}[level]
		prefix := fmt.Sprintf("hw.perflevel%d.", level)
		return fmt.Appendf(nil, "%sname: %s\n%sphysicalcpu: %d\n%slogicalcpu: %d\n", prefix, name, prefix, cores, prefix, cores), nil
	}
	result := c.Inventory(context.Background())
	if result.Status != "ok" {
		t.Fatal(result.Error)
	}
	if len(result.Data.CoreGroups) != 3 || result.Data.CoreGroups[0].Name != "Super" || result.Data.CoreGroups[2].PhysicalCores != 6 {
		t.Fatalf("core groups lost: %+v", result.Data.CoreGroups)
	}
}

func TestUnprivilegedSecurityDoesNotInvokeSystemsetup(t *testing.T) {
	c := New("test")
	c.agent.UID = 501
	c.run = func(_ context.Context, path string, _ ...string) ([]byte, error) {
		if strings.HasSuffix(path, "systemsetup") {
			t.Fatal("unprivileged collector invoked systemsetup")
		}
		return []byte("{}"), nil
	}
	result := c.security(context.Background())
	if result.RemoteLogin.Status != "permission_denied" || result.RemoteLogin.Data != nil {
		t.Fatal("unavailable remote login status was reported as a value")
	}
}

func TestToggleRejectsUnexpectedOutput(t *testing.T) {
	result := parseToggle([]byte("You need administrator access to run this tool."), nil, "Remote Login: On", "Remote Login: Off")
	if result.Status != "error" || result.Data != nil {
		t.Fatal("unrecognized output must not become false")
	}
}
