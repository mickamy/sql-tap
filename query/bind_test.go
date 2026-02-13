package query_test

import (
	"testing"

	"github.com/mickamy/sql-tap/query"
)

func TestBind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sql  string
		args []string
		want string
	}{
		{
			name: "no args",
			sql:  "SELECT 1",
			args: nil,
			want: "SELECT 1",
		},
		{
			name: "postgres numeric",
			sql:  "SELECT * FROM users WHERE id = $1",
			args: []string{"42"},
			want: "SELECT * FROM users WHERE id = 42",
		},
		{
			name: "postgres string",
			sql:  "SELECT * FROM users WHERE name = $1",
			args: []string{"alice"},
			want: "SELECT * FROM users WHERE name = 'alice'",
		},
		{
			name: "postgres mixed",
			sql:  "SELECT * FROM users WHERE id = $1 AND name = $2",
			args: []string{"42", "alice"},
			want: "SELECT * FROM users WHERE id = 42 AND name = 'alice'",
		},
		{
			name: "postgres 10+ args",
			sql:  "INSERT INTO t VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)",
			args: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"},
			want: "INSERT INTO t VALUES (1, 2, 3, 4, 5, 6, 7, 8, 9, 10)",
		},
		{
			name: "mysql numeric",
			sql:  "SELECT * FROM users WHERE id = ?",
			args: []string{"42"},
			want: "SELECT * FROM users WHERE id = 42",
		},
		{
			name: "mysql string",
			sql:  "SELECT * FROM users WHERE name = ?",
			args: []string{"alice"},
			want: "SELECT * FROM users WHERE name = 'alice'",
		},
		{
			name: "mysql more placeholders than args",
			sql:  "SELECT ? AND ? AND ?",
			args: []string{"1", "2"},
			want: "SELECT 1 AND 2 AND ?",
		},
		{
			name: "quote escaping",
			sql:  "SELECT * FROM users WHERE name = $1",
			args: []string{"O'Brien"},
			want: "SELECT * FROM users WHERE name = 'O''Brien'",
		},
		{
			name: "boolean not quoted",
			sql:  "SELECT * FROM users WHERE active = $1",
			args: []string{"true"},
			want: "SELECT * FROM users WHERE active = true",
		},
		{
			name: "null not quoted",
			sql:  "SELECT * FROM users WHERE name = $1",
			args: []string{"NULL"},
			want: "SELECT * FROM users WHERE name = NULL",
		},
		{
			name: "float not quoted",
			sql:  "SELECT * FROM t WHERE price > $1",
			args: []string{"3.14"},
			want: "SELECT * FROM t WHERE price > 3.14",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := query.Bind(tt.sql, tt.args)
			if got != tt.want {
				t.Errorf("Bind(%q, %v) = %q, want %q", tt.sql, tt.args, got, tt.want)
			}
		})
	}
}
