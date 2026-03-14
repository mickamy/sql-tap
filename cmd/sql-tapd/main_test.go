package main

import (
	"testing"

	"github.com/mickamy/sql-tap/proxy"
)

func TestIsSelectQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		op   proxy.Op
		q    string
		want bool
	}{
		{
			name: "regular select",
			op:   proxy.OpQuery,
			q:    "SELECT id FROM users WHERE id = 1",
			want: true,
		},
		{
			name: "metadata: SELECT database()",
			op:   proxy.OpQuery,
			q:    "SELECT database()",
			want: false,
		},
		{
			name: "metadata: SELECT @@version",
			op:   proxy.OpQuery,
			q:    "SELECT @@version",
			want: false,
		},
		{
			name: "metadata: SELECT 1",
			op:   proxy.OpQuery,
			q:    "SELECT 1",
			want: false,
		},
		{
			name: "metadata: SELECT NOW()",
			op:   proxy.OpQuery,
			q:    "SELECT NOW()",
			want: false,
		},
		{
			name: "metadata: SELECT current_database()",
			op:   proxy.OpQuery,
			q:    "SELECT current_database()",
			want: false,
		},
		{
			name: "select with FROM (not metadata)",
			op:   proxy.OpQuery,
			q:    "SELECT 1 FROM dual",
			want: true,
		},
		{
			name: "insert not select",
			op:   proxy.OpQuery,
			q:    "INSERT INTO users VALUES (1)",
			want: false,
		},
		{
			name: "begin op",
			op:   proxy.OpBegin,
			q:    "",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isSelectQuery(tt.op, tt.q)
			if got != tt.want {
				t.Errorf("isSelectQuery(%v, %q) = %v, want %v", tt.op, tt.q, got, tt.want)
			}
		})
	}
}

func TestIsMetadataQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		q    string
		want bool
	}{
		{
			name: "no FROM clause",
			q:    "SELECT database()",
			want: true,
		},
		{
			name: "system variable",
			q:    "SELECT @@session.transaction_read_only",
			want: true,
		},
		{
			name: "constant",
			q:    "SELECT 1",
			want: true,
		},
		{
			name: "has FROM clause",
			q:    "SELECT id FROM users",
			want: false,
		},
		{
			name: "subquery with FROM",
			q:    "SELECT (SELECT COUNT(*) FROM orders)",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isMetadataQuery(tt.q)
			if got != tt.want {
				t.Errorf("isMetadataQuery(%q) = %v, want %v", tt.q, got, tt.want)
			}
		})
	}
}
