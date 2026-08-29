package cli

import (
	"context"
	"fmt"
	"io"
)

var Version = "0.1.0-dev"

// Exit-code contract: exitUsage for command-line usage errors, exitFailure
// for runtime and environment failures (including a missing or invalid
// configuration), exitOK for success — including zero candidates, a not-due
// scheduled sync, and a nothing-to-remove uninstall.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func Run(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		printUsage(stderr)
		return exitUsage
	}
	switch arguments[0] {
	case "help", "--help", "-h":
		printUsage(stdout)
		return exitOK
	case "version", "--version":
		fmt.Fprintln(stdout, Version)
		return exitOK
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
		return exitUsage
	}
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, `spolia adds currently free OpenRouter and ZenMux models as selectable models in Kimi Code.

Get started:              spolia setup
Refresh available models: spolia sync
Check status and health:  spolia doctor
Remove spolia's changes:  spolia uninstall

Other commands: help, version
Run 'spolia <command> --help' for a command's options.`)
}
