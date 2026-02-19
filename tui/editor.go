package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mickamy/sql-tap/explain"
)

type editorResultMsg struct {
	query string
	args  []string
	mode  explain.Mode
	err   error
}

func openEditor(query string, args []string, mode explain.Mode) tea.Cmd {
	f, err := os.CreateTemp("", "sql-tap-*.sql")
	if err != nil {
		return func() tea.Msg {
			return editorResultMsg{err: err, mode: mode}
		}
	}
	path := f.Name()

	header := fmt.Sprintf(
		"-- Edit this query, then save and quit to run %s.\n"+
			"-- To cancel, clear the file or quit without saving.\n"+
			"-- Lines starting with -- are stripped before execution.\n\n",
		mode,
	)

	if _, err := f.WriteString(header + query); err != nil {
		_ = f.Close()
		_ = os.Remove(path) //nolint:gosec // path is a controlled temp file created by this function
		return func() tea.Msg {
			return editorResultMsg{err: err, mode: mode}
		}
	}
	_ = f.Close()

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	c := exec.CommandContext(context.Background(), editor, path) //nolint:gosec // $EDITOR is user-controlled by design
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	return tea.ExecProcess(c, func(err error) tea.Msg {
		defer func() { _ = os.Remove(path) }()

		if err != nil {
			return editorResultMsg{err: err, mode: mode}
		}

		edited, err := os.ReadFile(path) //nolint:gosec // path is our own temp file
		if err != nil {
			return editorResultMsg{err: err, mode: mode}
		}

		q := stripComments(string(edited))
		return editorResultMsg{
			query: q,
			args:  args,
			mode:  mode,
		}
	})
}

// stripComments removes SQL single-line comments (-- ...) and trims whitespace.
func stripComments(s string) string {
	split := strings.Split(s, "\n")
	lines := make([]string, 0, len(split))
	for line := range strings.SplitSeq(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
