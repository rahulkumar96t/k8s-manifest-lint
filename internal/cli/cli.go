// Package cli implements the k8s-manifest-lint command line.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"text/tabwriter"

	"github.com/rahulkumar96t/k8s-manifest-lint/internal/config"
	"github.com/rahulkumar96t/k8s-manifest-lint/internal/lint"
	"github.com/rahulkumar96t/k8s-manifest-lint/internal/manifest"
	"github.com/rahulkumar96t/k8s-manifest-lint/internal/report"
)

// Exit codes.
const (
	ExitOK       = 0 // no findings at or above --fail-on
	ExitFindings = 1 // at least one finding at or above --fail-on
	ExitError    = 2 // usage, config, read or parse error
)

// Version is set at build time with -ldflags "-X .../internal/cli.Version=v1.2.3".
// When empty, the module version recorded by `go install` is used.
var Version = ""

func version() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

const usage = `Usage: k8s-manifest-lint [flags] [path ...]

Lints Kubernetes manifests. Paths may be files or directories (searched
recursively for *.yaml and *.yml); "-" or no path reads standard input:

  helm template my-app ./chart | k8s-manifest-lint
  kustomize build overlays/prod | k8s-manifest-lint --format sarif -o lint.sarif

Flags:
  -c, --config FILE    config file (default: .k8s-lint.yaml in the working
                       directory, if present)
  -f, --format FORMAT  text, json or sarif (default text)
  -o, --output FILE    write the report to FILE instead of stdout
      --fail-on LEVEL  lowest severity that fails the run: error, warning,
                       info or none (default: failOn from the config, else warning)
      --list-rules     print the rules with their effective severity and exit
      --version        print the version and exit

Exit status: 0 clean, 1 findings at or above --fail-on, 2 usage or parse error.
`

// Run executes the command and returns the process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("k8s-manifest-lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	var (
		configPath, format, output, failOn string
		listRules, showVersion             bool
	)
	fs.StringVar(&configPath, "config", "", "config file (default: "+config.DefaultFile+" in the working directory, if present)")
	fs.StringVar(&configPath, "c", "", "shorthand for --config")
	fs.StringVar(&format, "format", "text", "output format: "+strings.Join(report.Formats, ", "))
	fs.StringVar(&format, "f", "text", "shorthand for --format")
	fs.StringVar(&output, "output", "", "write the report to this file instead of stdout")
	fs.StringVar(&output, "o", "", "shorthand for --output")
	fs.StringVar(&failOn, "fail-on", "", `lowest severity that fails the run: error, warning, info or none (default "warning")`)
	fs.BoolVar(&listRules, "list-rules", false, "print the rules with their effective severity and exit")
	fs.BoolVar(&showVersion, "version", false, "print the version and exit")

	paths, err := parseInterleaved(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return ExitOK
	}
	if err != nil {
		return ExitError // the flag package has already printed the error
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "k8s-manifest-lint: %v\n", err)
		return ExitError
	}
	if showVersion {
		fmt.Fprintln(stdout, version())
		return ExitOK
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fail(err)
	}
	linter, err := lint.New(cfg)
	if err != nil {
		return fail(err)
	}
	if listRules {
		printRules(stdout, linter)
		return ExitOK
	}
	threshold, err := resolveFailOn(failOn, cfg.FailOn)
	if err != nil {
		return fail(err)
	}
	if !validFormat(format) {
		return fail(fmt.Errorf("unknown format %q (want %s)", format, strings.Join(report.Formats, ", ")))
	}
	if len(paths) == 0 {
		if isTerminal(stdin) {
			fs.Usage()
			return ExitError
		}
		paths = []string{"-"}
	}

	objs, err := manifest.Load(paths, stdin)
	if err != nil {
		return fail(err)
	}
	res, err := linter.Run(objs)
	if err != nil {
		return fail(err)
	}
	if err := writeReport(output, stdout, format, res); err != nil {
		return fail(err)
	}
	if threshold != lint.None && res.Count(threshold) > 0 {
		return ExitFindings
	}
	return ExitOK
}

// parseInterleaved allows flags after paths ("lint dir/ --format json"),
// which the standard flag package does not. Everything after "--" is a path.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var tail []string
	for i, a := range args {
		if a == "--" {
			args, tail = args[:i], args[i+1:]
			break
		}
	}
	var paths []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return append(paths, tail...), nil
		}
		paths = append(paths, args[0])
		args = args[1:]
	}
}

func resolveFailOn(flagValue, configValue string) (lint.Severity, error) {
	v := flagValue
	if v == "" {
		v = configValue
	}
	if v == "" {
		v = "warning"
	}
	sev, err := lint.ParseSeverity(v)
	if err != nil {
		return 0, fmt.Errorf("--fail-on: %w", err)
	}
	return sev, nil
}

func validFormat(f string) bool {
	for _, known := range report.Formats {
		if f == known {
			return true
		}
	}
	return false
}

func writeReport(output string, stdout io.Writer, format string, res *lint.Result) error {
	if output == "" {
		return report.Write(stdout, format, res, version())
	}
	f, err := os.Create(output)
	if err != nil {
		return err
	}
	if err := report.Write(f, format, res, version()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func printRules(w io.Writer, l *lint.Linter) {
	effective := map[string]lint.Severity{}
	for _, r := range l.Rules() {
		effective[r.ID] = r.Severity
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSEVERITY\tCATEGORY\tSUMMARY")
	for _, r := range lint.Registry() {
		sev := "off"
		if s, ok := effective[r.ID]; ok {
			sev = s.String()
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.ID, sev, r.Category, r.Summary)
	}
	tw.Flush()
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
