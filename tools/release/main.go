package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/Mtrya/llmloot/internal/distribution"
)

func main() {
	version := flag.String("version", "", "release version, with or without a leading v")
	output := flag.String("output", "dist", "empty output directory")
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if flag.NArg() != 0 || *version == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := (distribution.Builder{Root: *root, Output: *output, Version: *version}).Build(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
