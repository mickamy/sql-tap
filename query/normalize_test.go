package query_test

import (
	"testing"

	"github.com/mickamy/sql-tap/query"
)

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"string literal", "SELECT id FROM users WHERE name = 'alice'", "SELECT id FROM users WHERE name = '?'"},
		{"escaped quote", "WHERE name = 'it''s'", "WHERE name = '?'"},
		{"numeric literal", "SELECT id, name FROM users WHERE id = 42", "SELECT id, name FROM users WHERE id = ?"},
		{"float literal", "WHERE score > 3.14", "WHERE score > ?"},
		{"pg param kept", "WHERE id = $1 AND name = $2", "WHERE id = $1 AND name = $2"},
		{"in list", "WHERE id IN (1, 2, 3)", "WHERE id IN (?, ?, ?)"},
		{"mixed", "WHERE id = 42 AND name = 'bob' AND status = $1", "WHERE id = ? AND name = '?' AND status = $1"},
		{"whitespace collapse", "SELECT  id\n\tFROM  users", "SELECT id FROM users"},
		{"leading trailing space", "  SELECT 1  ", "SELECT ?"},
		{"no replace in identifier", "SELECT t1.id FROM t1", "SELECT t1.id FROM t1"},
		{"negative number", "WHERE x = -5", "WHERE x = -?"},
		{"multiple string literals", "INSERT INTO t (a, b) VALUES ('x', 'y')", "INSERT INTO t (a, b) VALUES ('?', '?')"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := query.Normalize(tt.in)
			if got != tt.want {
				t.Errorf("Normalize(%q)\n got  %q\n want %q", tt.in, got, tt.want)
			}
		})
	}
}
