package testing

import (
	"path/filepath"
	"testing"
)

func TestIsMusterExecutable(t *testing.T) {
	for path, want := range map[string]bool{
		filepath.Join("usr", "local", "bin", "muster"): true,
		filepath.Join("build", "muster.exe"):           true,
		filepath.Join("tmp", "testing.test"):           false,
		filepath.Join("bin", "muster-agent"):           false,
	} {
		if got := isMusterExecutable(path); got != want {
			t.Errorf("isMusterExecutable(%q) = %v, want %v", path, got, want)
		}
	}
}
