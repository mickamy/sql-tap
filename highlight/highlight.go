package highlight

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
)

var (
	lexer     chroma.Lexer
	formatter chroma.Formatter
	style     *chroma.Style
)

func init() {
	lexer = lexers.Get("sql")
	formatter = formatters.Get("terminal256")
	style = styles.Get("monokai")
}

// SQL returns the input with ANSI terminal syntax highlighting applied.
// On error or empty input, the original string is returned unchanged.
func SQL(s string) string {
	if s == "" {
		return s
	}

	iterator, err := lexer.Tokenise(nil, s)
	if err != nil {
		return s
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return s
	}

	return strings.TrimRight(buf.String(), "\n")
}

var (
	nodeRe = regexp.MustCompile(
		//nolint:dupword // regex alternatives, not duplicate words
		`(?i)\b(Seq Scan|Index Scan|Index Only Scan|Bitmap Heap Scan|Bitmap Index Scan|` +
			`Incremental Sort|Sort|Hash Join|Merge Join|Nested Loop|Hash|` +
			`WindowAgg|Aggregate|Group|Limit|Unique|Gather Merge|Gather|` +
			`Materialize|Append|Result|Subquery Scan|CTE Scan|Function Scan|Values Scan|` +
			`LockRows|SetOp|ModifyTable|` +
			`Table scan|Index lookup|Covering index|Full scan|ref|range|ALL|index|const)\b`,
	)
	metricsRe = regexp.MustCompile(`\((?:cost|actual time|loops|width|never executed)[^)]*\)`)
	arrowRe   = regexp.MustCompile(`->`)
	summaryRe = regexp.MustCompile(`(?i)^\s*(Planning Time|Execution Time|Query time):`)

	boldStyle = lipgloss.NewStyle().Bold(true)
	dimStyle  = lipgloss.NewStyle().Faint(true)
)

// Plan returns the EXPLAIN output with ANSI highlighting applied.
// Node names are bold, metrics are dim, arrows are dim, and summary lines are bold.
func Plan(s string) string {
	if s == "" {
		return s
	}

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if summaryRe.MatchString(line) {
			lines[i] = boldStyle.Render(line)
			continue
		}

		line = arrowRe.ReplaceAllStringFunc(line, func(m string) string {
			return dimStyle.Render(m)
		})
		line = metricsRe.ReplaceAllStringFunc(line, func(m string) string {
			return dimStyle.Render(m)
		})
		line = nodeRe.ReplaceAllStringFunc(line, func(m string) string {
			return boldStyle.Render(m)
		})
		lines[i] = line
	}

	return strings.Join(lines, "\n")
}
