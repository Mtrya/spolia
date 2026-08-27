package cli

import (
	"context"
	"fmt"
	"io"
)

var Version = "0.1.0-dev"

func Run(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		printUsage(stderr)
		return 2
	}
	switch arguments[0] {
	case "help", "--help", "-h":
		printUsage(stdout)
		return 0
	case "version", "--version":
		fmt.Fprintln(stdout, Version)
		return 0
	case "setup":
		return runSetup(ctx, arguments[1:], stdout, stderr)
	case "sync":
		return runSync(ctx, arguments[1:], stdout, stderr)
	case "doctor":
		return runDoctor(ctx, arguments[1:], stdout, stderr)
	case "uninstall":
		return runUninstall(ctx, arguments[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", arguments[0])
		printUsage(stderr)
		return 2
	}
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: spolia <setup|sync|doctor|uninstall|help|version>")
}
