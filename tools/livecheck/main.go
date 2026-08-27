package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Mtrya/llmloot/internal/source"
	"github.com/Mtrya/llmloot/internal/source/openrouter"
	"github.com/Mtrya/llmloot/internal/source/zenmux"
)

func main() {
	sourceName := flag.String("source", "all", "source to check: openrouter, zenmux, or all")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "livecheck does not accept positional arguments")
		os.Exit(2)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	checks := []check{
		{name: "openrouter", environmentName: "OPENROUTER_API_KEY", adapter: openrouter.New(client)},
		{name: "zenmux", environmentName: "ZENMUX_API_KEY", adapter: zenmux.New(client)},
	}
	if *sourceName != "all" && *sourceName != "openrouter" && *sourceName != "zenmux" {
		fmt.Fprintf(os.Stderr, "unknown source %q\n", *sourceName)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	failed := false
	for _, current := range checks {
		if *sourceName != "all" && *sourceName != current.name {
			continue
		}
		credential := os.Getenv(current.environmentName)
		if credential == "" {
			fmt.Fprintf(os.Stderr, "%s: missing %s\n", current.name, current.environmentName)
			failed = true
			continue
		}
		catalog, err := current.adapter.Fetch(ctx, credential)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", current.name, err)
			failed = true
			continue
		}
		invalid := 0
		for _, count := range catalog.InvalidRecords {
			invalid += count
		}
		fmt.Printf("%s: models=%d invalid_records=%d\n", current.name, len(catalog.Models), invalid)
	}
	if failed {
		os.Exit(1)
	}
}

type check struct {
	name            string
	environmentName string
	adapter         source.Adapter
}
