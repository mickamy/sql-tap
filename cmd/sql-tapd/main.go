package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/mickamy/sql-tap/broker"
	"github.com/mickamy/sql-tap/dsn"
	"github.com/mickamy/sql-tap/explain"
	"github.com/mickamy/sql-tap/proxy"
	"github.com/mickamy/sql-tap/proxy/mysql"
	"github.com/mickamy/sql-tap/proxy/postgres"
	"github.com/mickamy/sql-tap/server"
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

	if err := run(*driver, *listen, *upstream, *grpcAddr, *dsnEnv); err != nil {
		log.Fatal(err)
	}
}

func run(driver, listen, upstream, grpcAddr, dsnEnv string) error {
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

	go func() {
		for ev := range p.Events() {
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
