// document-evaluation exports an existing execution capture entirely offline.
// It does not initialize the Gateway, connect to models, or repeat a task.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/executionevidence"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("document-evaluation", flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("evidence-dir", "", "private capture directory (trace_dir/document-evidence)")
	owner := flags.String("owner", "", "expected owner ID")
	session := flags.String("session", "", "expected session ID")
	runID := flags.String("run", "", "expected run ID")
	output := flags.String("output", "", "new output ZIP path; existing files are never overwritten")
	diagnostic := flags.Bool("diagnostic", false, "export available evidence with explicit incomplete-capture warnings")
	maxBytes := flags.Int64("max-export-bytes", executionevidence.DefaultOptions().MaxExportBytes, "maximum raw and archive export bytes")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *directory == "" || *owner == "" || *session == "" || *runID == "" || *output == "" || *maxBytes <= 0 {
		fmt.Fprintln(stderr, "provide --evidence-dir --owner --session --run --output; limits must be positive")
		return 2
	}
	scope := executionevidence.Scope{OwnerID: *owner, SessionID: *session, RunID: *runID}
	opts := executionevidence.DefaultOptions()
	opts.MaxExportBytes = *maxBytes
	ctx, cancel := context.WithTimeout(context.Background(), opts.ExportTimeout)
	defer cancel()
	recorder := executionevidence.New(*directory, opts)
	manifest, err := recorder.Export(ctx, scope, *output, *diagnostic)
	if err != nil {
		fmt.Fprintln(stderr, "evaluation export failed:", err)
		return 1
	}
	result := struct {
		RunID           string                         `json:"run_id"`
		Output          string                         `json:"output"`
		CaptureComplete bool                           `json:"capture_complete"`
		PackageComplete bool                           `json:"package_complete"`
		Review          executionevidence.ReviewStatus `json:"review"`
	}{scope.RunID, *output, manifest.Capture.Complete, manifest.Export.Complete, manifest.Review}
	if err = json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "cannot write export result:", err)
		return 1
	}
	return 0
}
