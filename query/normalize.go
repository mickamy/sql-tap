package query

import "strings"

// Normalize replaces literal values in a SQL query with placeholders,
// so that structurally identical queries can be grouped together.
//
// String literals ('...') are replaced with '?', standalone numeric
// literals are replaced with ?, and $N parameters are kept as-is.
// Consecutive whitespace is collapsed to a single space.
func Normalize(sql string) string {
	if sql == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(sql))

	i := 0
	prevSpace := false
	for i < len(sql) {
		ch := sql[i]

		if ch == '\'' {
			i = normalizeString(&b, sql, i)
			prevSpace = false
			continue
		}

		if ch == '$' && i+1 < len(sql) && isDigit(sql[i+1]) {
			i = keepParam(&b, sql, i)
			prevSpace = false
			continue
		}

		if isDigit(ch) && (i == 0 || isNumBoundary(sql[i-1])) {
			if next, ok := normalizeNumber(&b, sql, i); ok {
				i = next
				prevSpace = false
				continue
			}
		}

		if isSpace(ch) {
			if !prevSpace && b.Len() > 0 {
				b.WriteByte(' ')
				prevSpace = true
			}
			i++
			continue
		}

		b.WriteByte(ch)
		i++
		prevSpace = false
	}

	return strings.TrimRight(b.String(), " ")
}

// normalizeString replaces a string literal starting at pos with '?'.
func normalizeString(b *strings.Builder, sql string, pos int) int {
	j := pos + 1
	for j < len(sql) {
		if sql[j] == '\'' && j+1 < len(sql) && sql[j+1] == '\'' {
			j += 2
			continue
		}
		if sql[j] == '\'' {
			j++
			break
		}
		j++
	}
	b.WriteString("'?'")
	return j
}

// keepParam writes $N parameter as-is and returns the new position.
func keepParam(b *strings.Builder, sql string, pos int) int {
	b.WriteByte('$')
	j := pos + 1
	for j < len(sql) && isDigit(sql[j]) {
		b.WriteByte(sql[j])
		j++
	}
	return j
}

// normalizeNumber replaces a numeric literal at pos with '?'.
// Returns (newPos, true) if replaced, or (0, false) if not a standalone number.
func normalizeNumber(b *strings.Builder, sql string, pos int) (int, bool) {
	j := pos + 1
	for j < len(sql) && (isDigit(sql[j]) || sql[j] == '.') {
		j++
	}
	if j >= len(sql) || isNumBoundary(sql[j]) {
		b.WriteByte('?')
		return j, true
	}
	return 0, false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isNumBoundary(c byte) bool {
	return isSpace(c) ||
		c == ',' || c == '(' || c == ')' || c == '=' ||
		c == '<' || c == '>' || c == '+' || c == '-' ||
		c == '*' || c == '/' || c == ';'
}
