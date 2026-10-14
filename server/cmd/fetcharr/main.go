// Command fetcharr is the Fetcharr server entry point.
package main

import (
	"flag"
	"log/slog"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("fetcharr", flag.ContinueOnError)
	cfgPath := fs.String("config", os.Getenv("FETCHARR_CONFIG"), "path to config.yaml (env FETCHARR_CONFIG)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	log.Info("fetcharr starting", "config", *cfgPath)
	log.Info("fetcharr exiting (not implemented yet)")
	return 0
}
