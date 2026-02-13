package clipboard_test

import (
	"os/exec"
	"runtime"
	"testing"

	"github.com/mickamy/sql-tap/clipboard"
)

func TestCopy(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("clipboard not supported on %s", runtime.GOOS)
	}

	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("pbcopy"); err != nil {
			t.Skip("pbcopy not found")
		}
	case "linux":
		if _, err := exec.LookPath("xclip"); err != nil {
			if _, err := exec.LookPath("xsel"); err != nil {
				t.Skip("xclip/xsel not found")
			}
		}
	}

	if err := clipboard.Copy(t.Context(), "hello from test"); err != nil {
		t.Fatalf("Copy returned error: %v", err)
	}
}
