package explain_test

import (
	"testing"

	"github.com/mickamy/sql-tap/explain"
)

func TestDetectDriver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dsn     string
		want    string
		wantErr bool
	}{
		{name: "postgres URI", dsn: "postgres://user:pass@localhost/db", want: "pgx"},
		{name: "postgresql URI", dsn: "postgresql://user:pass@localhost/db", want: "pgx"},
		{name: "postgres key=value", dsn: "host=localhost dbname=db", want: "pgx"},
		{name: "mysql", dsn: "user:pass@tcp(localhost:3306)/db", want: "mysql"},
		{name: "empty", dsn: "", wantErr: true},
		{name: "unknown", dsn: "foobar", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := explain.DetectDriver(tt.dsn)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMode_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode explain.Mode
		want string
	}{
		{explain.Explain, "EXPLAIN"},
		{explain.Analyze, "EXPLAIN ANALYZE"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			if got := tt.mode.String(); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
