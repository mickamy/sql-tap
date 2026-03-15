package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/mickamy/sql-tap/broker"
	"github.com/mickamy/sql-tap/config"
	"github.com/mickamy/sql-tap/detect"
	"github.com/mickamy/sql-tap/dsn"
	"github.com/mickamy/sql-tap/explain"
	"github.com/mickamy/sql-tap/proxy"
	"github.com/mickamy/sql-tap/proxy/mysql"
	"github.com/mickamy/sql-tap/proxy/postgres"
	"github.com/mickamy/sql-tap/query"
	"github.com/mickamy/sql-tap/server"
	"github.com/mickamy/sql-tap/web"
)

var version = "dev"

func main() {
	fs := flag.NewFlagSet("sql-tapd", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr,
			"sql-tapd — SQL proxy daemon for sql-tap\n\nUsage:\n  sql-tapd [flags]\n\nFlags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr,
			"\nEnvironment:\n  DATABASE_URL    DSN for EXPLAIN queries (read by default via -dsn-env)\n")
	}

	configPath := fs.String("config", "", "path to config file (default: .sql-tap.yaml)")
	driver := fs.String("driver", "", "database driver: postgres, mysql, tidb (required)")
	listen := fs.String("listen", "", "client listen address (required)")
	upstream := fs.String("upstream", "", "upstream database address (required)")
	grpcAddr := fs.String("grpc", ":9091", "gRPC server address for TUI")
	dsnEnv := fs.String("dsn-env", "DATABASE_URL", "environment variable holding DSN for EXPLAIN")
	httpAddr := fs.String("http", "", "HTTP server address for web UI (e.g. :8080)")
	nplus1Threshold := fs.Int("nplus1-threshold", 5, "N+1 detection threshold (0 to disable)")
	nplus1Window := fs.Duration("nplus1-window", time.Second, "N+1 detection time window")
	nplus1Cooldown := fs.Duration("nplus1-cooldown", 10*time.Second, "N+1 alert cooldown per query template")
	slowThreshold := fs.Duration("slow-threshold", 100*time.Millisecond, "slow query threshold (0 to disable)")
	showVersion := fs.Bool("version", false, "show version and exit")

	_ = fs.Parse(os.Args[1:])

	if *showVersion {
		fmt.Printf("sql-tapd %s\n", version)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	// CLI flags override config file values.
	set := flagsSet(fs)
	if set["driver"] && *driver != "" {
		cfg.Driver = *driver
	}
	if set["listen"] && *listen != "" {
		cfg.Listen = *listen
	}
	if set["upstream"] && *upstream != "" {
		cfg.Upstream = *upstream
	}
	if set["grpc"] && *grpcAddr != "" {
		cfg.GRPC = *grpcAddr
	}
	if set["dsn-env"] && *dsnEnv != "" {
		cfg.DSNEnv = *dsnEnv
	}
	if set["http"] {
		cfg.HTTP = *httpAddr
	}
	if set["nplus1-threshold"] {
		cfg.NPlus1.Threshold = *nplus1Threshold
	}
	if set["nplus1-window"] {
		cfg.NPlus1.Window = *nplus1Window
	}
	if set["nplus1-cooldown"] {
		cfg.NPlus1.Cooldown = *nplus1Cooldown
	}
	if set["slow-threshold"] {
		cfg.SlowThreshold = *slowThreshold
	}

	if cfg.Driver == "" || cfg.Listen == "" || cfg.Upstream == "" {
		fs.Usage()
		os.Exit(1)
	}

	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

// flagsSet returns the set of flag names explicitly passed on the command line.
func flagsSet(fs *flag.FlagSet) map[string]bool {
	m := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { m[f.Name] = true })
	return m
}

func run(cfg config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Broker
	b := broker.New(256)

	// EXPLAIN client (optional)
	var explainClient *explain.Client
	if raw := os.Getenv(cfg.DSNEnv); raw != "" {
		db, err := dsn.Open(raw)
		if err != nil {
			return fmt.Errorf("open db for explain: %w", err)
		}
		var explainDriver explain.Driver
		switch cfg.Driver {
		case "mysql":
			explainDriver = explain.MySQL
		case "tidb":
			explainDriver = explain.TiDB
		case "postgres":
			explainDriver = explain.Postgres
		}
		explainClient = explain.NewClient(db, explainDriver)
		defer func() { _ = explainClient.Close() }()
		log.Printf("EXPLAIN enabled")
	} else {
		log.Printf("EXPLAIN disabled (%s not set)", cfg.DSNEnv)
	}

	// gRPC server
	var lc net.ListenConfig
	grpcLis, err := lc.Listen(ctx, "tcp", cfg.GRPC)
	if err != nil {
		return fmt.Errorf("listen grpc %s: %w", cfg.GRPC, err)
	}
	srv := server.New(b, explainClient)
	go func() {
		log.Printf("gRPC server listening on %s", cfg.GRPC)
		if err := srv.Serve(grpcLis); err != nil {
			log.Printf("grpc serve: %v", err)
		}
	}()

	// HTTP server (optional)
	if cfg.HTTP != "" {
		httpLis, err := lc.Listen(ctx, "tcp", cfg.HTTP)
		if err != nil {
			return fmt.Errorf("listen http %s: %w", cfg.HTTP, err)
		}
		webSrv := web.New(b, explainClient)
		go func() {
			log.Printf("HTTP server listening on %s", cfg.HTTP)
			if err := webSrv.Serve(httpLis); err != nil {
				log.Printf("http serve: %v", err)
			}
		}()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = webSrv.Shutdown(shutdownCtx)
		}()
	}

	// Proxy
	var p proxy.Proxy
	switch cfg.Driver {
	case "postgres":
		p = postgres.New(cfg.Listen, cfg.Upstream)
	case "mysql", "tidb":
		p = mysql.New(cfg.Listen, cfg.Upstream)
	default:
		return fmt.Errorf("unsupported driver: %s", cfg.Driver)
	}

	// N+1 detector (optional)
	var det *detect.Detector
	if cfg.NPlus1.Threshold > 0 {
		det = detect.New(cfg.NPlus1.Threshold, cfg.NPlus1.Window, cfg.NPlus1.Cooldown)
		log.Printf("N+1 detection enabled (threshold=%d, window=%s, cooldown=%s)",
			cfg.NPlus1.Threshold, cfg.NPlus1.Window, cfg.NPlus1.Cooldown)
	}

	if cfg.SlowThreshold > 0 {
		log.Printf("slow query detection enabled (threshold=%s)", cfg.SlowThreshold)
	}

	go func() {
		for ev := range p.Events() {
			if ev.Query != "" {
				ev.NormalizedQuery = query.Normalize(ev.Query)
			}
			if det != nil && isSelectQuery(ev.Op, ev.Query) {
				r := det.Record(ev.Query, ev.StartTime)
				ev.NPlus1 = r.Matched
				if r.Alert != nil {
					log.Printf("N+1 detected: %q (%d times in %s)",
						r.Alert.Query, r.Alert.Count, cfg.NPlus1.Window)
				}
			}
			if cfg.SlowThreshold > 0 && ev.Duration >= cfg.SlowThreshold {
				ev.SlowQuery = true
			}
			b.Publish(ev)
		}
	}()

	log.Printf("proxying %s -> %s (driver=%s)", cfg.Listen, cfg.Upstream, cfg.Driver)
	if err := p.ListenAndServe(ctx); err != nil {
		return fmt.Errorf("proxy: %w", err)
	}

	srv.GracefulStop()
	return nil
}

func isSelectQuery(op proxy.Op, q string) bool {
	switch op {
	case proxy.OpQuery, proxy.OpExec, proxy.OpExecute:
		trimmed := strings.TrimSpace(q)
		if len(trimmed) < 6 || !strings.EqualFold(trimmed[:6], "SELECT") {
			return false
		}
		return !isMetadataQuery(trimmed)
	case proxy.OpPrepare, proxy.OpBind, proxy.OpBegin, proxy.OpCommit, proxy.OpRollback:
		return false
	}
	return false
}

// reFromClause matches the SQL FROM keyword as a whole word, used to detect
// whether a SELECT query references any table.
// NOTE: This is a simple keyword check; it cannot distinguish a FROM clause
// from FROM inside expressions (e.g. EXTRACT(EPOCH FROM NOW()) in Postgres).
// Such queries will not be classified as metadata, which is a safe default
// (they stay in N+1 detection rather than being silently dropped).
var reFromClause = regexp.MustCompile(`(?i)\bFROM\b`)

// isMetadataQuery reports whether q is a system/metadata SELECT that should be
// excluded from N+1 detection. These are selects that do not reference any
// table (i.e. have no FROM clause), such as SELECT database(), SELECT @@version,
// or SELECT 1.
func isMetadataQuery(q string) bool {
	return !reFromClause.MatchString(q)
}
