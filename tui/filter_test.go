package tui //nolint:testpackage // testing internal filter parsing logic

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	tapv1 "github.com/mickamy/sql-tap/gen/tap/v1"
	"github.com/mickamy/sql-tap/proxy"
)

func TestParseFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []filterCondition
	}{
		{
			name:  "empty",
			input: "",
			want:  nil,
		},
		{
			name:  "plain text",
			input: "users",
			want: []filterCondition{
				{kind: filterText, text: "users"},
			},
		},
		{
			name:  "duration greater than ms",
			input: "d>100ms",
			want: []filterCondition{
				{kind: filterDuration, durOp: durGT, durValue: 100 * time.Millisecond},
			},
		},
		{
			name:  "duration less than us",
			input: "d<500us",
			want: []filterCondition{
				{kind: filterDuration, durOp: durLT, durValue: 500 * time.Microsecond},
			},
		},
		{
			name:  "duration greater than s",
			input: "d>1s",
			want: []filterCondition{
				{kind: filterDuration, durOp: durGT, durValue: 1 * time.Second},
			},
		},
		{
			name:  "error keyword",
			input: "error",
			want: []filterCondition{
				{kind: filterError},
			},
		},
		{
			name:  "error keyword case insensitive",
			input: "Error",
			want: []filterCondition{
				{kind: filterError},
			},
		},
		{
			name:  "op:select",
			input: "op:select",
			want: []filterCondition{
				{kind: filterOp, opPattern: "select"},
			},
		},
		{
			name:  "op:begin",
			input: "op:begin",
			want: []filterCondition{
				{kind: filterOp, opPattern: "begin"},
			},
		},
		{
			name:  "combined filter",
			input: "op:select d>100ms",
			want: []filterCondition{
				{kind: filterOp, opPattern: "select"},
				{kind: filterDuration, durOp: durGT, durValue: 100 * time.Millisecond},
			},
		},
		{
			name:  "text with WHERE",
			input: "WHERE id",
			want: []filterCondition{
				{kind: filterText, text: "where"},
				{kind: filterText, text: "id"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseFilter(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("parseFilter(%q) returned %d conditions, want %d", tt.input, len(got), len(tt.want))
			}
			for i, g := range got {
				w := tt.want[i]
				if g.kind != w.kind {
					t.Errorf("cond[%d].kind = %d, want %d", i, g.kind, w.kind)
				}
				if g.text != w.text {
					t.Errorf("cond[%d].text = %q, want %q", i, g.text, w.text)
				}
				if g.durOp != w.durOp {
					t.Errorf("cond[%d].durOp = %d, want %d", i, g.durOp, w.durOp)
				}
				if g.durValue != w.durValue {
					t.Errorf("cond[%d].durValue = %v, want %v", i, g.durValue, w.durValue)
				}
				if g.opPattern != w.opPattern {
					t.Errorf("cond[%d].opPattern = %q, want %q", i, g.opPattern, w.opPattern)
				}
			}
		})
	}
}

func makeEvent(op proxy.Op, query string, dur time.Duration, errMsg string) *tapv1.QueryEvent {
	ev := &tapv1.QueryEvent{
		Op:    int32(op),
		Query: query,
	}
	if dur > 0 {
		ev.Duration = durationpb.New(dur)
	}
	if errMsg != "" {
		ev.Error = errMsg
	}
	return ev
}

func TestMatchesEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cond filterCondition
		ev   *tapv1.QueryEvent
		want bool
	}{
		{
			name: "text match",
			cond: filterCondition{kind: filterText, text: "users"},
			ev:   makeEvent(proxy.OpQuery, "SELECT id FROM users", 10*time.Millisecond, ""),
			want: true,
		},
		{
			name: "text no match",
			cond: filterCondition{kind: filterText, text: "orders"},
			ev:   makeEvent(proxy.OpQuery, "SELECT id FROM users", 10*time.Millisecond, ""),
			want: false,
		},
		{
			name: "duration GT match",
			cond: filterCondition{kind: filterDuration, durOp: durGT, durValue: 50 * time.Millisecond},
			ev:   makeEvent(proxy.OpQuery, "SELECT 1", 100*time.Millisecond, ""),
			want: true,
		},
		{
			name: "duration GT no match",
			cond: filterCondition{kind: filterDuration, durOp: durGT, durValue: 200 * time.Millisecond},
			ev:   makeEvent(proxy.OpQuery, "SELECT 1", 100*time.Millisecond, ""),
			want: false,
		},
		{
			name: "duration LT match",
			cond: filterCondition{kind: filterDuration, durOp: durLT, durValue: 200 * time.Millisecond},
			ev:   makeEvent(proxy.OpQuery, "SELECT 1", 100*time.Millisecond, ""),
			want: true,
		},
		{
			name: "duration LT no match",
			cond: filterCondition{kind: filterDuration, durOp: durLT, durValue: 50 * time.Millisecond},
			ev:   makeEvent(proxy.OpQuery, "SELECT 1", 100*time.Millisecond, ""),
			want: false,
		},
		{
			name: "error match",
			cond: filterCondition{kind: filterError},
			ev:   makeEvent(proxy.OpQuery, "SELECT 1", 10*time.Millisecond, "some error"),
			want: true,
		},
		{
			name: "error no match",
			cond: filterCondition{kind: filterError},
			ev:   makeEvent(proxy.OpQuery, "SELECT 1", 10*time.Millisecond, ""),
			want: false,
		},
		{
			name: "op:select match",
			cond: filterCondition{kind: filterOp, opPattern: "select"},
			ev:   makeEvent(proxy.OpQuery, "SELECT id FROM users", 10*time.Millisecond, ""),
			want: true,
		},
		{
			name: "op:select no match (insert)",
			cond: filterCondition{kind: filterOp, opPattern: "select"},
			ev:   makeEvent(proxy.OpQuery, "INSERT INTO users VALUES (1)", 10*time.Millisecond, ""),
			want: false,
		},
		{
			name: "op:begin match",
			cond: filterCondition{kind: filterOp, opPattern: "begin"},
			ev:   makeEvent(proxy.OpBegin, "", 0, ""),
			want: true,
		},
		{
			name: "op:begin no match",
			cond: filterCondition{kind: filterOp, opPattern: "begin"},
			ev:   makeEvent(proxy.OpCommit, "", 0, ""),
			want: false,
		},
		{
			name: "op:insert match",
			cond: filterCondition{kind: filterOp, opPattern: "insert"},
			ev:   makeEvent(proxy.OpQuery, "INSERT INTO users (name) VALUES ('alice')", 5*time.Millisecond, ""),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.cond.matchesEvent(tt.ev)
			if got != tt.want {
				t.Errorf("matchesEvent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchAllConditions(t *testing.T) {
	t.Parallel()

	ev := makeEvent(proxy.OpQuery, "SELECT id FROM users WHERE id = 1", 150*time.Millisecond, "")

	tests := []struct {
		name  string
		conds []filterCondition
		want  bool
	}{
		{
			name:  "empty conditions match everything",
			conds: nil,
			want:  true,
		},
		{
			name: "all match",
			conds: []filterCondition{
				{kind: filterOp, opPattern: "select"},
				{kind: filterDuration, durOp: durGT, durValue: 100 * time.Millisecond},
			},
			want: true,
		},
		{
			name: "one fails",
			conds: []filterCondition{
				{kind: filterOp, opPattern: "select"},
				{kind: filterDuration, durOp: durGT, durValue: 200 * time.Millisecond},
			},
			want: false,
		},
		{
			name: "text and op",
			conds: []filterCondition{
				{kind: filterOp, opPattern: "select"},
				{kind: filterText, text: "users"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := matchAllConditions(ev, tt.conds)
			if got != tt.want {
				t.Errorf("matchAllConditions() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWrapFooterItems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		items []string
		width int
		want  string
	}{
		{
			name:  "all fit in one line",
			items: []string{"a: foo", "b: bar"},
			width: 80,
			want:  "  a: foo  b: bar",
		},
		{
			name:  "wrap to two lines",
			items: []string{"a: foo", "b: bar", "c: baz"},
			width: 20,
			want:  "  a: foo  b: bar\n  c: baz",
		},
		{
			name:  "each item on its own line",
			items: []string{"long-item-1", "long-item-2", "long-item-3"},
			width: 18,
			want:  "  long-item-1\n  long-item-2\n  long-item-3",
		},
		{
			name:  "zero width falls back to single line",
			items: []string{"a", "b"},
			width: 0,
			want:  "  a  b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := wrapFooterItems(tt.items, tt.width)
			if got != tt.want {
				t.Errorf("wrapFooterItems(%v, %d) =\n%q\nwant:\n%q", tt.items, tt.width, got, tt.want)
			}
		})
	}
}

func TestDescribeFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "op:select and duration",
			input: "op:select d>100ms",
			want:  "op:select d>100ms",
		},
		{
			name:  "error keyword",
			input: "error",
			want:  "error",
		},
		{
			name:  "text fallback",
			input: "users",
			want:  "text:users",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := describeFilter(tt.input)
			if got != tt.want {
				t.Errorf("describeFilter(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
