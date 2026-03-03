package explain

import (
	"testing"
	"time"
)

func TestParseTimestampParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		want  map[int]bool
	}{
		{
			name:  "no casts",
			query: "SELECT * FROM users WHERE id = $1",
			want:  map[int]bool{},
		},
		{
			name:  "timestamp with time zone",
			query: "SELECT * FROM t WHERE created_at > $1::TIMESTAMP WITH TIME ZONE",
			want:  map[int]bool{1: true},
		},
		{
			name:  "timestamptz",
			query: "SELECT * FROM t WHERE ts = $2::TIMESTAMPTZ",
			want:  map[int]bool{2: true},
		},
		{
			name:  "timestamp without time zone",
			query: "SELECT * FROM t WHERE ts = $3::TIMESTAMP WITHOUT TIME ZONE",
			want:  map[int]bool{3: true},
		},
		{
			name:  "plain timestamp",
			query: "SELECT * FROM t WHERE ts = $1::TIMESTAMP",
			want:  map[int]bool{1: true},
		},
		{
			name:  "lowercase cast",
			query: "SELECT * FROM t WHERE ts > $1::timestamp with time zone",
			want:  map[int]bool{1: true},
		},
		{
			name:  "spaces around ::",
			query: "SELECT * FROM t WHERE ts = $1 :: TIMESTAMP",
			want:  map[int]bool{1: true},
		},
		{
			name:  "mixed: timestamp and non-timestamp",
			query: "SELECT * FROM t WHERE key = $1::VARCHAR AND ts > $2::TIMESTAMP WITH TIME ZONE",
			want:  map[int]bool{2: true},
		},
		{
			name:  "multiple timestamp params",
			query: "SELECT * FROM t WHERE a > $1::TIMESTAMP AND b < $3::TIMESTAMPTZ",
			want:  map[int]bool{1: true, 3: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := parseTimestampParams(tt.query)
			if len(got) != len(tt.want) {
				t.Fatalf("parseTimestampParams(%q) = %v, want %v", tt.query, got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("parseTimestampParams(%q)[%d] = %v, want %v", tt.query, k, got[k], v)
				}
			}
		})
	}
}

func TestParsePGTimestamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    time.Time
		wantOK  bool
	}{
		{
			name:   "PostgreSQL epoch (zero)",
			input:  "0",
			want:   time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
			wantOK: true,
		},
		{
			name:   "large microseconds value (issue repro: ~2026)",
			input:  "825505830505628",
			want: func() time.Time {
				microsecs := int64(825505830505628)
				sec := microsecs / 1_000_000
				usec := microsecs % 1_000_000
				return time.Unix(sec+pgEpochUnix, usec*1_000).UTC()
			}(),
			wantOK: true,
		},
		{
			name:   "negative (before 2000-01-01)",
			input:  "-1000000",
			want:   time.Date(1999, 12, 31, 23, 59, 59, 0, time.UTC),
			wantOK: true,
		},
		{
			name:   "negative fractional (before 2000-01-01)",
			input:  "-1",
			want:   time.Date(1999, 12, 31, 23, 59, 59, 999999000, time.UTC),
			wantOK: true,
		},
		{
			name:   "non-integer string",
			input:  "2026-02-27T14:10:30Z",
			wantOK: false,
		},
		{
			name:   "float string",
			input:  "1.5",
			wantOK: false,
		},
		{
			name:   "empty string",
			input:  "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parsePGTimestamp(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("parsePGTimestamp(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if ok && !got.Equal(tt.want) {
				t.Errorf("parsePGTimestamp(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildAnyArgs(t *testing.T) {
	t.Parallel()

	pgEpoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		query string
		args  []string
		check func(t *testing.T, got []any)
	}{
		{
			name:  "no args",
			query: "SELECT 1",
			args:  nil,
			check: func(t *testing.T, got []any) {
				t.Helper()
				if len(got) != 0 {
					t.Errorf("expected empty slice, got %v", got)
				}
			},
		},
		{
			name:  "non-timestamp arg stays as string",
			query: "SELECT * FROM users WHERE id = $1",
			args:  []string{"42"},
			check: func(t *testing.T, got []any) {
				t.Helper()
				if s, ok := got[0].(string); !ok || s != "42" {
					t.Errorf("got[0] = %v (%T), want string %q", got[0], got[0], "42")
				}
			},
		},
		{
			name:  "timestamp arg converted to time.Time",
			query: "SELECT * FROM t WHERE expired_at > $1::TIMESTAMP WITH TIME ZONE",
			args:  []string{"825505830505628"},
			check: func(t *testing.T, got []any) {
				t.Helper()
				ts, ok := got[0].(time.Time)
				if !ok {
					t.Fatalf("got[0] = %v (%T), want time.Time", got[0], got[0])
				}
				// The value should be ~2026 (pgEpoch + 825505830.5 seconds)
				if ts.Before(pgEpoch) {
					t.Errorf("got time %v, expected a time after 2000-01-01", ts)
				}
			},
		},
		{
			name:  "non-integer timestamp arg stays as string",
			query: "SELECT * FROM t WHERE ts > $1::TIMESTAMP WITH TIME ZONE",
			args:  []string{"2026-02-27T14:10:30Z"},
			check: func(t *testing.T, got []any) {
				t.Helper()
				if s, ok := got[0].(string); !ok || s != "2026-02-27T14:10:30Z" {
					t.Errorf("got[0] = %v (%T), want string %q", got[0], got[0], "2026-02-27T14:10:30Z")
				}
			},
		},
		{
			name:  "mixed args: varchar and timestamp",
			query: "SELECT * FROM t WHERE key = $1::VARCHAR AND ts > $2::TIMESTAMP WITH TIME ZONE",
			args:  []string{"019c5c4f-f25a-772b-97d4-1646a125080d", "825505830505628"},
			check: func(t *testing.T, got []any) {
				t.Helper()
				if s, ok := got[0].(string); !ok || s != "019c5c4f-f25a-772b-97d4-1646a125080d" {
					t.Errorf("got[0] = %v (%T), want string", got[0], got[0])
				}
				if _, ok := got[1].(time.Time); !ok {
					t.Errorf("got[1] = %v (%T), want time.Time", got[1], got[1])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := buildAnyArgs(tt.query, tt.args)
			tt.check(t, got)
		})
	}
}
