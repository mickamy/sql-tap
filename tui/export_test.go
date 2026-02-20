package tui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	tapv1 "github.com/mickamy/sql-tap/gen/tap/v1"
	"github.com/mickamy/sql-tap/proxy"
)

func makeExportEvent(
	op proxy.Op, query string, args []string,
	dur time.Duration, startTime time.Time,
) *tapv1.QueryEvent {
	ev := &tapv1.QueryEvent{
		Op:        int32(op),
		Query:     query,
		Args:      args,
		StartTime: timestamppb.New(startTime),
	}
	if dur > 0 {
		ev.Duration = durationpb.New(dur)
	}
	return ev
}

func testEvents() []*tapv1.QueryEvent {
	base := time.Date(2026, 2, 20, 15, 4, 5, 123000000, time.UTC)
	return []*tapv1.QueryEvent{
		makeExportEvent(proxy.OpQuery,
			"SELECT id FROM users WHERE email = $1",
			[]string{"alice@example.com"},
			152300*time.Microsecond, base),
		makeExportEvent(proxy.OpQuery,
			"SELECT id FROM users WHERE email = $1",
			[]string{"bob@example.com"},
			203100*time.Microsecond, base.Add(time.Second)),
		makeExportEvent(proxy.OpExec,
			"INSERT INTO orders (user_id) VALUES ($1)",
			[]string{"1"},
			50*time.Millisecond, base.Add(2*time.Second)),
	}
}

func TestRenderMarkdown(t *testing.T) {
	t.Parallel()

	events := testEvents()
	md := renderMarkdown(events, "", "")

	checks := []string{
		"# sql-tap export",
		"- Captured: 3 queries",
		"- Exported: 3 queries",
		"## Queries",
		"| # | Time | Op | Duration | Query | Args | Error |",
		"SELECT id FROM users WHERE email = $1",
		"['alice@example.com']",
		"INSERT INTO orders",
		"## Analytics",
		"| Query | Count | Total | Avg |",
	}

	for _, want := range checks {
		if !strings.Contains(md, want) {
			t.Errorf("renderMarkdown output missing %q\n\nGot:\n%s",
				want, md)
		}
	}
}

func TestRenderMarkdownFiltered(t *testing.T) {
	t.Parallel()

	events := testEvents()
	md := renderMarkdown(events, "op:select", "")

	if !strings.Contains(md, "- Captured: 3 queries") {
		t.Error("should show total captured count")
	}
	if !strings.Contains(md, "- Exported: 2 queries") {
		t.Error("should show filtered exported count")
	}
	if !strings.Contains(md, "(filter: op:select)") {
		t.Error("should show active filter")
	}
	if strings.Contains(md, "INSERT INTO orders") {
		t.Error("should not include non-matching events")
	}
}

func TestRenderJSON(t *testing.T) {
	t.Parallel()

	events := testEvents()
	out, err := renderJSON(events, "op:select", "users")
	if err != nil {
		t.Fatalf("renderJSON error: %v", err)
	}

	var d exportData
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("JSON decode error: %v", err)
	}

	if d.Captured != 3 {
		t.Errorf("captured = %d, want 3", d.Captured)
	}
	if d.Exported != 2 {
		t.Errorf("exported = %d, want 2", d.Exported)
	}
	if d.Filter != "op:select" {
		t.Errorf("filter = %q, want %q", d.Filter, "op:select")
	}
	if d.Search != "users" {
		t.Errorf("search = %q, want %q", d.Search, "users")
	}
	if len(d.Queries) != 2 {
		t.Errorf("queries count = %d, want 2", len(d.Queries))
	}
	if len(d.Analytics) != 1 {
		t.Errorf("analytics count = %d, want 1", len(d.Analytics))
	}
	if len(d.Analytics) > 0 && d.Analytics[0].Count != 2 {
		t.Errorf("analytics[0].count = %d, want 2",
			d.Analytics[0].Count)
	}
}

func TestRenderJSONEmptyArgs(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 2, 20, 15, 0, 0, 0, time.UTC)
	events := []*tapv1.QueryEvent{
		makeExportEvent(proxy.OpQuery, "SELECT 1", nil,
			10*time.Millisecond, base),
	}

	out, err := renderJSON(events, "", "")
	if err != nil {
		t.Fatalf("renderJSON error: %v", err)
	}

	var d exportData
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("JSON decode error: %v", err)
	}

	if d.Queries[0].Args == nil {
		t.Error("args should be empty array, not null")
	}
	if len(d.Queries[0].Args) != 0 {
		t.Errorf("args length = %d, want 0", len(d.Queries[0].Args))
	}
}

func TestWriteExport(t *testing.T) {
	t.Parallel()

	events := testEvents()
	dir := t.TempDir()

	t.Run("markdown", func(t *testing.T) {
		t.Parallel()
		path, err := writeExport(events, "", "",
			exportMarkdown, dir)
		if err != nil {
			t.Fatalf("writeExport error: %v", err)
		}
		if !strings.HasSuffix(path, ".md") {
			t.Errorf("path %q should end with .md", path)
		}

		data, err := os.ReadFile(path) //nolint:gosec // test file
		if err != nil {
			t.Fatalf("read file error: %v", err)
		}
		if !strings.Contains(string(data), "# sql-tap export") {
			t.Error("written file should contain markdown header")
		}
	})

	t.Run("json", func(t *testing.T) {
		t.Parallel()
		path, err := writeExport(events, "", "",
			exportJSON, dir)
		if err != nil {
			t.Fatalf("writeExport error: %v", err)
		}
		if !strings.HasSuffix(path, ".json") {
			t.Errorf("path %q should end with .json", path)
		}

		data, err := os.ReadFile(path) //nolint:gosec // test file
		if err != nil {
			t.Fatalf("read file error: %v", err)
		}
		var d exportData
		if err := json.Unmarshal(data, &d); err != nil {
			t.Fatalf("JSON decode error: %v", err)
		}
		if d.Captured != 3 {
			t.Errorf("captured = %d, want 3", d.Captured)
		}
	})
}

func TestBuildExportAnalytics(t *testing.T) {
	t.Parallel()

	events := testEvents()
	rows := buildExportAnalytics(events)

	if len(rows) != 2 {
		t.Fatalf("analytics rows = %d, want 2", len(rows))
	}

	// First row should be the SELECT query (appears first)
	if rows[0].Count != 2 {
		t.Errorf("rows[0].count = %d, want 2", rows[0].Count)
	}
	if !strings.Contains(rows[0].Query, "SELECT") {
		t.Errorf("rows[0].query = %q, want SELECT query",
			rows[0].Query)
	}

	// Second row should be the INSERT query
	if rows[1].Count != 1 {
		t.Errorf("rows[1].count = %d, want 1", rows[1].Count)
	}
}

func TestEscapeMarkdownPipe(t *testing.T) {
	t.Parallel()

	got := escapeMarkdownPipe("a | b | c")
	want := "a \\| b \\| c"
	if got != want {
		t.Errorf("escapeMarkdownPipe = %q, want %q", got, want)
	}
}
