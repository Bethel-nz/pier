package localname

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxLoginItemIsEnabledAndRemoved(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	unit := filepath.Join(config, "systemd", "user", systemdUnitName)
	link := filepath.Join(config, "systemd", "user", "default.target.wants", systemdUnitName)

	for i := 0; i < 2; i++ { // twice: the second run must be a no-op
		if err := setLoginItem("/usr/local/bin/pier", true); err != nil {
			t.Fatal(err)
		}
	}
	if target, err := os.Readlink(link); err != nil || target != unit {
		t.Fatalf("enable link = %q, %v", target, err)
	}
	if err := setLoginItem("/usr/local/bin/pier", false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{unit, link} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("%s survived removal", path)
		}
	}
}
