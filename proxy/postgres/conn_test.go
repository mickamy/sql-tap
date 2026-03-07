package postgres_test

import (
	"encoding/binary"
	"testing"
	"time"

	pgproxy "github.com/mickamy/sql-tap/proxy/postgres"
)

func TestDecodeBinaryParam(t *testing.T) {
	t.Parallel()

	// Encode microseconds as big-endian 8 bytes, matching PostgreSQL binary format.
	encodeMicros := func(us int64) []byte {
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(us))
		return b
	}

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
			oid:      pgproxy.OidTimestamp,
			wantTime: true,
		},
		{
			name:     "timestamptz OID decodes as RFC3339",
			data:     encodeMicros(826159500119733),
			oid:      pgproxy.OidTimestampTZ,
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
