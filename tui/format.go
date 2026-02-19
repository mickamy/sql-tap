package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mickamy/sql-tap/proxy"
)

func formatTimeFull(t *timestamppb.Timestamp) string {
	if t == nil {
		return "-"
	}
	return t.AsTime().In(time.Local).Format("15:04:05") //nolint:gosmopolitan // TUI displays local time
}

func opString(op int32) string {
	return proxy.Op(op).String()
}

func padRight(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func padLeft(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return strings.Repeat(" ", width-w) + s
}

var reSpaces = regexp.MustCompile(`\s+`)

func truncate(s string, maxLen int) string {
	s = strings.TrimSpace(reSpaces.ReplaceAllString(s, " "))
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return s[:maxLen]
	}
	return s[:maxLen-1] + "…"
}

func formatDuration(d *durationpb.Duration) string {
	if d == nil {
		return "-"
	}
	return formatDurationValue(d.AsDuration())
}

func formatDurationValue(dur time.Duration) string {
	switch {
	case dur < time.Millisecond:
		us := float64(dur.Microseconds())
		return fmt.Sprintf("%.0fµs", us)
	case dur < time.Second:
		ms := float64(dur.Microseconds()) / 1000
		return fmt.Sprintf("%.1fms", ms)
	}
	return fmt.Sprintf("%.2fs", dur.Seconds())
}

func formatTime(t *timestamppb.Timestamp) string {
	if t == nil {
		return "-"
	}
	return t.AsTime().In(time.Local).Format("15:04:05.000") //nolint:gosmopolitan // TUI displays local time
}

// renderInputWithCursor renders a text input with a block cursor at the given rune position.
func renderInputWithCursor(text string, cursorPos int) string {
	runes := []rune(text)
	if cursorPos >= len(runes) {
		return text + "█"
	}
	return string(runes[:cursorPos]) + "█" + string(runes[cursorPos:])
}

func friendlyError(err error, width int) string {
	msg := err.Error()

	var text string
	switch {
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "Unavailable"):
		text = "Could not connect to sql-tapd.\n" +
			"Is sql-tapd running?\n\n" +
			"Error: " + msg
	}
	if text == "" {
		text = "Error: " + msg
	}

	return lipgloss.NewStyle().Width(width).Render(text)
}
