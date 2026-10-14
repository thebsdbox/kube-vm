package virtio

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStaleTapNames(t *testing.T) {
	dir := t.TempDir()
	for name, state := range map[string]string{
		"tap0":  "down",
		"tap1":  "up",
		"tap42": "unknown",
		"eth0":  "up",
		"notap": "down",
	} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "operstate"), []byte(state), 0o644); err != nil {
			t.Fatalf("write operstate %s: %v", name, err)
		}
	}

	got, err := staleTapNames(dir)
	if err != nil {
		t.Fatalf("staleTapNames: %v", err)
	}

	want := []string{"tap0", "tap42"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staleTapNames() = %#v, want %#v", got, want)
	}
}
