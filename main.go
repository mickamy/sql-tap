package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mickamy/sql-tap/ci"
	"github.com/mickamy/sql-tap/tui"
)

var version = "dev"

func main() {
	fs := flag.NewFlagSet("sql-tap", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "sql-tap — Watch SQL traffic in real-time\n\nUsage:\n  sql-tap [flags] <addr>\n\nFlags:\n")
		fs.PrintDefaults()
	}

	showVersion := fs.Bool("version", false, "show version and exit")
	ciMode := fs.Bool("ci", false, "run in CI mode: collect events until SIGTERM/SIGINT, then report and exit")

	_ = fs.Parse(os.Args[1:])

	if *showVersion {
		fmt.Printf("sql-tap %s\n", version)
		return
	}

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}

	addr := fs.Arg(0)
	if *ciMode {
		runCI(addr)
	} else {
		monitor(addr)
	}
}

func monitor(addr string) {
	m := tui.New(addr)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runCI(addr string) {
	os.Exit(runCIExitCode(addr))
}

func runCIExitCode(addr string) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	result, err := ci.Run(ctx, addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	fmt.Fprint(os.Stderr, result.Report())

	if result.HasProblems() {
		return 1
	}
	return 0
}
