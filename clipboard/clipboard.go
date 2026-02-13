package clipboard

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Copy writes text to the system clipboard.
// It uses pbcopy on macOS, xclip/xsel on Linux, and clip.exe on Windows.
func Copy(ctx context.Context, text string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "pbcopy")
	case "linux":
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.CommandContext(ctx, "xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.CommandContext(ctx, "xsel", "--clipboard", "--input")
		} else {
			return errors.New("xclip or xsel is required on Linux")
		}
	case "windows":
		cmd = exec.CommandContext(ctx, "clip.exe")
	}

	if cmd == nil {
		return fmt.Errorf("clipboard not supported on %s", runtime.GOOS)
	}

	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clipboard copy: %w", err)
	}
	return nil
}
