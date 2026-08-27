package main

import (
	"context"
	"os"
	"runtime/debug"
	"strings"

	"github.com/Mtrya/llmloot/internal/cli"
)

func main() {
	if info, ok := debug.ReadBuildInfo(); ok && cli.Version == "0.1.0-dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		cli.Version = strings.TrimPrefix(info.Main.Version, "v")
	}
	os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
