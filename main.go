package main

import (
	"flag"
	"fmt"
	"os"
)

var version = "dev"

func main() {
	fs := flag.NewFlagSet("sql-tap", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "sql-tap — Watch SQL traffic in real-time\n\nUsage:\n  sql-tap [flags] <addr>\n\nFlags:\n")
		fs.PrintDefaults()
	}

	showVersion := fs.Bool("version", false, "show version and exit")

	_ = fs.Parse(os.Args[1:])

	if *showVersion {
		fmt.Printf("sql-tap %s\n", version)
		return
	}

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(1)
	}

	monitor(fs.Arg(0))
}

func monitor(addr string) {
	fmt.Fprintf(os.Stdout, "not implemented yet\n")
}
