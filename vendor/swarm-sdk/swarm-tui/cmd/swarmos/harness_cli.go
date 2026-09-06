package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
)

var errHarnessCommandFailed = errors.New("harness command failed")

// discoverHarnessPath applies the bounded harness discovery contract. An
// explicit path always wins. The explicit value "." is an opt-in shorthand for
// the exact local ./harness.yaml; discovery never walks parent directories.
func discoverHarnessPath(explicitPath string) (string, bool) {
	if explicitPath != "" && explicitPath != "." {
		return explicitPath, true
	}

	localPath := filepath.Join(".", harness.DefaultManifestName)
	if _, err := os.Stat(localPath); err == nil {
		return localPath, true
	}
	return "", false
}

func runHarnessSubcommand(args []string) error {
	return runHarnessSubcommandIO(args, os.Stdout, os.Stderr)
}

func runHarnessSubcommandIO(args []string, stdout, stderr io.Writer) error {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "usage: swarm harness <validate|explain> PATH")
		return errHarnessCommandFailed
	}

	command, path := args[0], args[1]
	switch command {
	case "validate":
		plan, err := harness.Compile(path)
		if err != nil {
			writeHarnessDiagnostics(stderr, err)
			return errHarnessCommandFailed
		}
		fmt.Fprintf(stdout, "OK %s\n", plan.Digest())
		return nil

	case "explain":
		plan, err := harness.Compile(path)
		if err != nil {
			writeHarnessDiagnostics(stderr, err)
			return errHarnessCommandFailed
		}
		output := struct {
			BuildIdentity harnessBuildIdentity  `json:"buildIdentity"`
			Plan          harness.ExplainReport `json:"plan"`
		}{
			BuildIdentity: currentHarnessBuildIdentity(),
			Plan:          plan.Explain(),
		}
		encoded, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			writeHarnessDiagnostics(stderr, err)
			return errHarnessCommandFailed
		}
		_, _ = fmt.Fprintln(stdout, string(encoded))
		return nil

	default:
		fmt.Fprintf(stderr, "unknown harness command %q; expected validate or explain\n", command)
		return errHarnessCommandFailed
	}
}

type harnessBuildIdentity struct {
	Version   string `json:"version"`
	BuildID   string `json:"buildId"`
	GitCommit string `json:"gitCommit"`
	BuildTime string `json:"buildTime"`
}

func currentHarnessBuildIdentity() harnessBuildIdentity {
	return harnessBuildIdentity{
		Version:   version.Version,
		BuildID:   version.BuildID,
		GitCommit: version.GitCommit,
		BuildTime: version.BuildTime,
	}
}

func writeHarnessDiagnostics(w io.Writer, err error) {
	diagnostics, ok := harness.AsDiagnostics(err)
	if !ok {
		diagnostics = harness.Diagnostics{{
			Severity: harness.SeverityError,
			Code:     "harness.cli.failed",
			Message:  "harness command failed without printable diagnostics",
		}}
	}
	output := struct {
		OK          bool                `json:"ok"`
		Diagnostics harness.Diagnostics `json:"diagnostics"`
	}{
		OK:          false,
		Diagnostics: diagnostics,
	}
	encoded, marshalErr := json.MarshalIndent(output, "", "  ")
	if marshalErr != nil {
		fmt.Fprintln(w, `{"ok":false,"diagnostics":[{"severity":"error","code":"harness.cli.failed","message":"harness command failed"}]}`)
		return
	}
	_, _ = fmt.Fprintln(w, string(encoded))
}

// buildHarnessClientOptions returns only the options needed for closed harness
// construction. A yolo plan does not enable itself: the distinct CLI gesture
// must set allowYolo before WithHarnessAllowYolo is appended. client.New owns
// the authoritative D2 rejection and performs it before resource construction.
func buildHarnessClientOptions(plan *harness.Plan, allowYolo bool) ([]client.Option, error) {
	if plan == nil {
		return nil, fmt.Errorf("harness: nil plan")
	}

	options := []client.Option{client.WithHarnessPlan(plan)}
	if allowYolo {
		options = append(options, client.WithHarnessAllowYolo())
	}
	return options, nil
}

func harnessFlagSelected(value string) bool {
	return strings.TrimSpace(value) != ""
}
