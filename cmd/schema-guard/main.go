// Command schema-guard reports breaking changes between two versions of a
// JSON Schema and exits non-zero when it finds one, so it can gate CI.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/joseph-caughey-labs/schema-guard/schemadiff"
)

const (
	exitOK       = 0
	exitBreaking = 1
	exitUsage    = 2
)

const usage = `usage: schema-guard [flags] OLD NEW

Compares two versions of a JSON Schema and reports changes that would break
existing readers (-mode output), existing senders (-mode input), or either
(-mode both, the default). Flags go before the file names.

Exit status: 0 no breaking changes, 1 breaking changes found, 2 usage error.

Flags:
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("schema-guard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	modeFlag := fs.String("mode", "both", "output, input or both")
	pointer := fs.String("pointer", "", "JSON Pointer to the schema inside each file, e.g. /components/schemas/Order")
	format := fs.String("format", "text", "text or json")
	strict := fs.Bool("strict", false, "treat changes marked for review as breaking")
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return exitUsage
	}
	mode, err := schemadiff.ParseMode(*modeFlag)
	if err != nil {
		return fail(stderr, err)
	}
	if *format != "text" && *format != "json" {
		return fail(stderr, fmt.Errorf("unknown format %q: expected text or json", *format))
	}

	before, err := schemadiff.LoadFile(fs.Arg(0), *pointer)
	if err != nil {
		return fail(stderr, err)
	}
	after, err := schemadiff.LoadFile(fs.Arg(1), *pointer)
	if err != nil {
		return fail(stderr, err)
	}

	changes := schemadiff.Compare(before, after, mode)
	counts := map[schemadiff.Severity]int{}
	for _, c := range changes {
		counts[c.Severity]++
	}

	if *format == "json" {
		err = writeJSON(stdout, mode, changes, counts)
	} else {
		err = writeText(stdout, mode, changes, counts)
	}
	if err != nil {
		return fail(stderr, err)
	}

	if counts[schemadiff.Breaking] > 0 || (*strict && counts[schemadiff.Review] > 0) {
		return exitBreaking
	}
	return exitOK
}

func fail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "schema-guard: %v\n", err)
	return exitUsage
}

func writeText(w io.Writer, mode schemadiff.Mode, changes []schemadiff.Change, counts map[schemadiff.Severity]int) error {
	if len(changes) == 0 {
		_, err := fmt.Fprintf(w, "no changes (mode: %s)\n", mode)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range changes {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Severity, c.Path, c.Message)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%d breaking, %d review, %d non-breaking (mode: %s)\n",
		counts[schemadiff.Breaking], counts[schemadiff.Review], counts[schemadiff.NonBreaking], mode)
	return err
}

func writeJSON(w io.Writer, mode schemadiff.Mode, changes []schemadiff.Change, counts map[schemadiff.Severity]int) error {
	if changes == nil {
		changes = []schemadiff.Change{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Mode        string              `json:"mode"`
		Breaking    int                 `json:"breaking"`
		Review      int                 `json:"review"`
		NonBreaking int                 `json:"nonBreaking"`
		Changes     []schemadiff.Change `json:"changes"`
	}{mode.String(), counts[schemadiff.Breaking], counts[schemadiff.Review], counts[schemadiff.NonBreaking], changes})
}
