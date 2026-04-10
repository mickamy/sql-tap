package dsn_test

import (
	"testing"

	"github.com/mickamy/sql-tap/dsn"
)

func TestDetectDriver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "postgres URI", raw: "postgres://user:pass@localhost/db", want: "pgx"},     //nolint:gosec // test data
		{name: "postgresql URI", raw: "postgresql://user:pass@localhost/db", want: "pgx"}, //nolint:gosec // test data
		{name: "postgres key=value", raw: "host=localhost dbname=db", want: "pgx"},
		{name: "mysql", raw: "user:pass@tcp(localhost:3306)/db", want: "mysql"},
		{name: "empty", raw: "", wantErr: true},
		{name: "unknown", raw: "foobar", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := dsn.DetectDriver(tt.raw)
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

func TestMySQLURLToDriverDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "already has tcp wrapper",
			raw:  "user:pass@tcp(localhost:3306)/db",
			want: "user:pass@tcp(localhost:3306)/db",
		},
		{
			name: "bare host:port gets tcp wrapper",
			raw:  "user:pass@localhost:3306/db",
			want: "user:pass@tcp(localhost:3306)/db",
		},
		{
			name: "complex password with special chars",
			raw:  `root:bbbH.A.|?ZAAAAi*RN)<*9(xxx@localhost:3306/test`, //nolint:gosec // test data
			want: `root:bbbH.A.|?ZAAAAi*RN)<*9(xxx@tcp(localhost:3306)/test`,
		},
		{
			name: "password with @ sign uses last @ for boundary",
			raw:  "user:pass@word@localhost:3306/db",
			want: "user:pass@word@tcp(localhost:3306)/db",
		},
		{
			name: "unix socket unchanged",
			raw:  "user:pass@unix(/tmp/mysql.sock)/db",
			want: "user:pass@unix(/tmp/mysql.sock)/db",
		},
		{
			name: "with query params",
			raw:  "user:pass@localhost:3306/db?timeout=5s",
			want: "user:pass@tcp(localhost:3306)/db?timeout=5s",
		},
		{
			name: "no @ sign unchanged",
			raw:  "localhost:3306/db",
			want: "localhost:3306/db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := dsn.MySQLURLToDriverDSN(tt.raw)
			if got != tt.want {
				t.Errorf("MySQLURLToDriverDSN(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
