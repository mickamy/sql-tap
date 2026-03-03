package postgres_test

import (
	"testing"
	"time"

	pgproxy "github.com/mickamy/sql-tap/proxy/postgres"
)

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
