package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/vishu42/megagega/internal/pkgflow"
)

type analyzerFunc func(context.Context, string, pkgflow.Options) (pkgflow.Graph, error)
type writeFileFunc func(string, []byte, uint32) error

func main() {
	writeFile := func(path string, data []byte, perm uint32) error {
		return os.WriteFile(path, data, os.FileMode(perm))
	}

	if err := run(context.Background(), os.Args, os.Stdout, os.Stderr, pkgflow.AnalyzeDir, writeFile); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, analyze analyzerFunc, writeFile writeFileFunc) error {
	if len(args) == 0 {
		args = []string{"pkgflow"}
	}

	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)

	dir := flags.String("dir", ".", "directory containing the Go module to analyze")
	out := flags.String("out", "", "output file path; stdout when empty")
	format := flags.String("format", "html", "output format: html or dot")
	includeExternal := flags.Bool("include-external", false, "include packages outside the current module")

	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("dir is required")
	}
	if *format != "html" && *format != "dot" {
		return fmt.Errorf(`format must be "html" or "dot"`)
	}

	graph, err := analyze(ctx, *dir, pkgflow.Options{IncludeExternal: *includeExternal})
	if err != nil {
		return fmt.Errorf("analyze packages: %w", err)
	}

	var rendered string
	switch *format {
	case "html":
		rendered, err = pkgflow.RenderHTML(graph)
	case "dot":
		rendered = pkgflow.RenderDOT(graph)
	}
	if err != nil {
		return fmt.Errorf("render %s: %w", *format, err)
	}

	if *out == "" {
		_, err = io.WriteString(stdout, rendered)
		return err
	}
	if writeFile == nil {
		return fmt.Errorf("write file function is required")
	}
	if err := writeFile(*out, []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("write output file: %w", err)
	}
	return nil
}
