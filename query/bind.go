package query

import (
	"fmt"
	"strconv"
	"strings"
)

// Bind replaces placeholders in a SQL query with the provided args.
// It supports PostgreSQL-style ($1, $2, ...) and MySQL-style (?) placeholders.
func Bind(sql string, args []string) string {
	if len(args) == 0 {
		return sql
	}

	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = quoteArg(a)
	}

	// Try PostgreSQL-style first: $1, $2, ...
	// Replace in reverse order to avoid $1 matching inside $10.
	pg := sql
	replaced := false
	for i := len(quoted); i >= 1; i-- {
		placeholder := fmt.Sprintf("$%d", i)
		if strings.Contains(pg, placeholder) {
			replaced = true
			pg = strings.ReplaceAll(pg, placeholder, quoted[i-1])
		}
	}
	if replaced {
		return pg
	}

	// Fall back to MySQL-style: ?
	result := &strings.Builder{}
	argIdx := 0
	for i := range len(sql) {
		if sql[i] == '?' && argIdx < len(quoted) {
			result.WriteString(quoted[argIdx])
			argIdx++
		} else {
			result.WriteByte(sql[i])
		}
	}
	return result.String()
}

// quoteArg wraps a non-numeric arg in single quotes, escaping internal quotes.
func quoteArg(s string) string {
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return s
	}
	if s == "true" || s == "false" || s == "null" || s == "NULL" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
