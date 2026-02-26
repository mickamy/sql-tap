**Title:** Show HN: sql-tap now has a browser-based Web UI and automatic N+1 query detection

**URL:** https://github.com/mickamy/sql-tap

**Text:**

Hi HN, I shared sql-tap here a few weeks ago — a transparent SQL proxy that captures every query and lets you inspect it in real-time. Thanks for the feedback last time.

Two big additions:

**Built-in Web UI** — Add `--http=:8080` and open your browser. It's a zero-dependency vanilla JS SPA embedded in the binary — nothing extra to install or deploy. Features:

- Real-time query stream via SSE
- SQL syntax highlighting
- Click to inspect details, run EXPLAIN / EXPLAIN ANALYZE
- Query statistics view with normalized grouping (count, errors, avg/total duration)
- Structured filter (`d>100ms`, `op:select`, `error`)
- Transaction grouping (collapsible)
- Slow query colorization
- Pause / Clear controls
- Export captured queries as JSON or Markdown
- Copy query with bound args
- N+1 detection (toast notification + row highlight)

**N+1 query detection** — sql-tap now automatically detects when the same SELECT template is executed 5+ times within 1 second (configurable). Both TUI and Web UI show flagged queries in real-time — every query in the pattern is marked, not just the first one. Thresholds, time window, and alert cooldown are all tunable via CLI flags. `--nplus1-threshold=0` to disable.

Other updates since v0.0.1:

**TUI improvements**
- Structured filter mode (`f`): `d>100ms`, `op:select`, `error`, combinable with AND logic
- Analytics view (`a`): aggregate queries by template, sort by total/count/avg duration
- Export to file (`w`): save captured queries as JSON or Markdown
- Copy with bound args (`C`): substitutes `$1`/`?` placeholders with actual values
- Sort by duration (`s`), half-page scrolling (`Ctrl+d`/`Ctrl+u`)
- Alert overlay for copy/export operations

**Database support**
- TiDB support (`--driver=tidb`)
- MySQL 9 compatibility
- Fixed PostgreSQL binary parameter decoding (UUIDs, etc.)

The proxy works the same way: point your app at sql-tapd instead of your database, no code changes needed. It parses the native wire protocol to capture queries, prepared statements, transactions, and errors transparently.

Written in Go, single binary, install via Homebrew (`brew install --cask mickamy/tap/sql-tap`) or `go install`. This is a solo side project — if you find it useful, a star on GitHub would mean a lot.
