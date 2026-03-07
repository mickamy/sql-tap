package postgres_test

import (
	"encoding/binary"
	"testing"
	"time"

	pgproxy "github.com/mickamy/sql-tap/proxy/postgres"
)

// encodeMicros encodes microseconds as big-endian 8 bytes (PostgreSQL binary timestamp format).
func encodeMicros(us int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(us)) //nolint:gosec // test helper: intentional signed→unsigned reinterpretation
	return b
}

func TestParameterDescriptionFlow(t *testing.T) {
	t.Parallel()

	t.Run("unnamed bind picks up ParameterDescription OIDs", func(t *testing.T) {
		t.Parallel()

		tc := pgproxy.NewTestConn()

		// Parse with OID=0 (server-inferred), then Describe unnamed statement.
		tc.HandleParse("", "SELECT id FROM t WHERE ts < $1", []uint32{0})
		tc.HandleDescribe("")

		// Server responds with the actual OID (timestamptz).
		tc.HandleParameterDescription([]uint32{pgproxy.OIDTimestampTZ})

		// Bind with binary timestamp — should decode as RFC3339 thanks to resolved OID.
		tc.HandleBind("", [][]byte{encodeMicros(826159500119733)}, []int16{1})

		args := tc.LastBindArgs()
		if len(args) != 1 {
			t.Fatalf("got %d args, want 1", len(args))
		}
		if _, err := time.Parse(time.RFC3339Nano, args[0]); err != nil {
			t.Errorf("arg = %q, want RFC3339 parseable string", args[0])
		}
	})

	t.Run("named statement uses per-statement OIDs", func(t *testing.T) {
		t.Parallel()

		tc := pgproxy.NewTestConn()

		// Parse named statement with OID=0.
		tc.HandleParse("s1", "SELECT id FROM t WHERE ts < $1", []uint32{0})
		tc.HandleDescribe("s1")

		// Server responds with actual OID.
		tc.HandleParameterDescription([]uint32{pgproxy.OIDTimestamp})

		// Bind referencing the named statement.
		tc.HandleBind("s1", [][]byte{encodeMicros(826159500119733)}, []int16{1})

		args := tc.LastBindArgs()
		if len(args) != 1 {
			t.Fatalf("got %d args, want 1", len(args))
		}
		if _, err := time.Parse(time.RFC3339Nano, args[0]); err != nil {
			t.Errorf("arg = %q, want RFC3339 parseable string", args[0])
		}
	})

	t.Run("named statement OIDs do not pollute unnamed bind", func(t *testing.T) {
		t.Parallel()

		tc := pgproxy.NewTestConn()

		// Parse + Describe unnamed statement with OID=0 (no ParameterDescription yet).
		tc.HandleParse("", "SELECT id FROM t WHERE id = $1", []uint32{0})

		// Parse + Describe named statement — server resolves as timestamp.
		tc.HandleParse("s1", "SELECT id FROM t WHERE ts < $1", []uint32{0})
		tc.HandleDescribe("s1")
		tc.HandleParameterDescription([]uint32{pgproxy.OIDTimestamp})

		// Bind unnamed — should still use OID=0 (not timestamp from s1).
		tc.HandleBind("", [][]byte{encodeMicros(42)}, []int16{1})

		args := tc.LastBindArgs()
		if len(args) != 1 {
			t.Fatalf("got %d args, want 1", len(args))
		}
		// OID=0 with 8-byte binary → plain int64, not RFC3339.
		if args[0] != "42" {
			t.Errorf("arg = %q, want %q (should not be treated as timestamp)", args[0], "42")
		}
	})

	t.Run("ReadyForQuery drains stale pending describes", func(t *testing.T) {
		t.Parallel()

		tc := pgproxy.NewTestConn()

		// Parse fails on server, Describe is skipped — no ParameterDescription arrives.
		tc.HandleParse("", "INVALID SQL", []uint32{0})
		tc.HandleDescribe("")

		// ReadyForQuery clears the stale entry.
		tc.HandleReadyForQuery()

		// Next cycle: new Parse + Describe for a real query.
		tc.HandleParse("", "SELECT id FROM t WHERE ts < $1", []uint32{0})
		tc.HandleDescribe("")
		tc.HandleParameterDescription([]uint32{pgproxy.OIDTimestampTZ})

		tc.HandleBind("", [][]byte{encodeMicros(826159500119733)}, []int16{1})

		args := tc.LastBindArgs()
		if len(args) != 1 {
			t.Fatalf("got %d args, want 1", len(args))
		}
		if _, err := time.Parse(time.RFC3339Nano, args[0]); err != nil {
			t.Errorf("arg = %q, want RFC3339 parseable string", args[0])
		}
	})
}

func TestDecodeBinaryParam(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		data       []byte
		oid        uint32
		wantTime   bool // true if the result should parse as RFC3339
		wantString string
	}{
		{
			name:     "timestamp OID decodes as RFC3339",
			data:     encodeMicros(826159500119733),
			oid:      pgproxy.OIDTimestamp,
			wantTime: true,
		},
		{
			name:     "timestamptz OID decodes as RFC3339",
			data:     encodeMicros(826159500119733),
			oid:      pgproxy.OIDTimestampTZ,
			wantTime: true,
		},
		{
			name:       "zero OID 8-byte value decoded as plain int64",
			data:       encodeMicros(826159500119733),
			oid:        0,
			wantTime:   false,
			wantString: "826159500119733",
		},
		{
			name:       "4-byte int32",
			data:       []byte{0, 0, 0, 42},
			oid:        0,
			wantString: "42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := pgproxy.DecodeBinaryParam(tt.data, tt.oid)
			if tt.wantTime {
				if _, err := time.Parse(time.RFC3339Nano, got); err != nil {
					t.Errorf("DecodeBinaryParam() = %q, want RFC3339 parseable string", got)
				}
			} else if tt.wantString != "" && got != tt.wantString {
				t.Errorf("DecodeBinaryParam() = %q, want %q", got, tt.wantString)
			}
		})
	}
}

func TestDecodePGTimestampMicros(t *testing.T) {
	t.Parallel()

	pgEpoch := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		microsecs int64
		want      time.Time
	}{
		{
			name:      "zero (PostgreSQL epoch)",
			microsecs: 0,
			want:      pgEpoch,
		},
		{
			name:      "issue repro value (~2026-02-27)",
			microsecs: 825505830505628,
			want:      pgEpoch.Add(time.Duration(825505830505628) * time.Microsecond),
		},
		{
			name:      "negative (before 2000-01-01)",
			microsecs: -1_000_000,
			want:      time.Date(1999, 12, 31, 23, 59, 59, 0, time.UTC),
		},
		{
			name:      "negative fractional microsecond",
			microsecs: -1,
			want:      time.Date(1999, 12, 31, 23, 59, 59, 999_999_000, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := time.Parse(time.RFC3339Nano, pgproxy.DecodePGTimestampMicros(tt.microsecs))
			if err != nil {
				t.Fatalf("DecodePGTimestampMicros(%d) returned unparseable string: %v", tt.microsecs, err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("DecodePGTimestampMicros(%d) = %v, want %v", tt.microsecs, got, tt.want)
			}
		})
	}
}
