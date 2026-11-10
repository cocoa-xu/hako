package identity

import "testing"

func TestUUIDsAreCanonicalStableAndDistinct(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := New()
		if !Valid(id) || id[14] != '4' || seen[id] {
			t.Fatal("invalid or duplicate UUID")
		}
		seen[id] = true
	}
	legacy := "0123456789abcdef0123456789abcdef"
	id := Normalize(legacy)
	if !Valid(id) || id[14] != '5' || Normalize(legacy) != id || Normalize(id) != id {
		t.Fatal("legacy identity is not stable")
	}
	for _, invalid := range []string{"", legacy, "../" + legacy, "00000000-0000-0000-0000-000000000000"} {
		if Valid(invalid) {
			t.Fatal("invalid UUID accepted")
		}
	}
}
