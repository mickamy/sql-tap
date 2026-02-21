package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/mickamy/sql-tap/broker"
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
		fmt.Fprintf(os.Stderr, "sql-tapd — SQL proxy daemon for sql-tap\n\nUsage:\n  sql-tapd [flags]\n\nFlags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nEnvironment:\n  DATABASE_URL    DSN for EXPLAIN queries (read by default via -dsn-env)\n")
	}

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

	if *driver == "" || *listen == "" || *upstream == "" {
		fs.Usage()
		os.Exit(1)
	}

	err := run(
		*driver, *listen, *upstream, *grpcAddr, *dsnEnv, *httpAddr,
		*nplus1Threshold, *nplus1Window, *nplus1Cooldown, *slowThreshold,
	)
	if err != nil {
		log.Fatal(err)
	}
}

func run(
	driver, listen, upstream, grpcAddr, dsnEnv, httpAddr string,
	nplus1Threshold int, nplus1Window, nplus1Cooldown, slowThreshold time.Duration,
) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Broker
	b := broker.New(256)

	// EXPLAIN client (optional)
	var explainClient *explain.Client
	if raw := os.Getenv(dsnEnv); raw != "" {
		db, err := dsn.Open(raw)
		if err != nil {
			return fmt.Errorf("open db for explain: %w", err)
		}
		var explainDriver explain.Driver
		switch driver {
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
		log.Printf("EXPLAIN disabled (%s not set)", dsnEnv)
	}

	// gRPC server
	var lc net.ListenConfig
	grpcLis, err := lc.Listen(ctx, "tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("listen grpc %s: %w", grpcAddr, err)
	}
	srv := server.New(b, explainClient)
	go func() {
		log.Printf("gRPC server listening on %s", grpcAddr)
		if err := srv.Serve(grpcLis); err != nil {
			log.Printf("grpc serve: %v", err)
		}
	}()

	// HTTP server (optional)
	if httpAddr != "" {
		httpLis, err := lc.Listen(ctx, "tcp", httpAddr)
		if err != nil {
			return fmt.Errorf("listen http %s: %w", httpAddr, err)
		}
		webSrv := web.New(b, explainClient)
		go func() {
			log.Printf("HTTP server listening on %s", httpAddr)
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
	switch driver {
	case "postgres":
		p = postgres.New(listen, upstream)
	case "mysql", "tidb":
		p = mysql.New(listen, upstream)
	default:
		return fmt.Errorf("unsupported driver: %s", driver)
	}

	// N+1 detector (optional)
	var det *detect.Detector
	if nplus1Threshold > 0 {
		det = detect.New(nplus1Threshold, nplus1Window, nplus1Cooldown)
		log.Printf("N+1 detection enabled (threshold=%d, window=%s, cooldown=%s)",
			nplus1Threshold, nplus1Window, nplus1Cooldown)
	}

	if slowThreshold > 0 {
		log.Printf("slow query detection enabled (threshold=%s)", slowThreshold)
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
						r.Alert.Query, r.Alert.Count, nplus1Window)
				}
			}
			if slowThreshold > 0 && ev.Duration >= slowThreshold {
				ev.SlowQuery = true
			}
			b.Publish(ev)
		}
	}()

	log.Printf("proxying %s -> %s (driver=%s)", listen, upstream, driver)
	if err := p.ListenAndServe(ctx); err != nil {
		return fmt.Errorf("proxy: %w", err)
	}

	srv.GracefulStop()
	return nil
}

func isSelectQuery(op proxy.Op, query string) bool {
	switch op {
	case proxy.OpQuery, proxy.OpExec, proxy.OpExecute:
		q := strings.TrimSpace(query)
		return len(q) >= 6 && strings.EqualFold(q[:6], "SELECT")
	case proxy.OpPrepare, proxy.OpBind, proxy.OpBegin, proxy.OpCommit, proxy.OpRollback:
		return false
	}
	return false
}
