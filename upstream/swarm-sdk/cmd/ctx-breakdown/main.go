// cmd/ctx-breakdown — analyze a captured provider request body and report
// where the tokens go: system-prompt sections, tool/skill schemas, messages.
//
// This is the offline half of the context-budget tooling. It consumes the
// real wire payload that `swarmos --raw` dumps to stderr (the REQUEST BODY
// block), so the numbers reflect exactly what the TUI sent — including the
// tool JSON schemas that the [PROMPT PROVENANCE] banner alone does not size.
//
// # Capture
//
//	swarmos --raw 2> raw.log     # then send one message in the TUI and quit
//	# isolate the JSON object printed after "REQUEST BODY" into req.json
//
// # Usage
//
//	go run ./cmd/ctx-breakdown req.json                 # text, from a file
//	pbpaste | go run ./cmd/ctx-breakdown                # text, from stdin
//	go run ./cmd/ctx-breakdown -open req.json           # also open an HTML report
//	go run ./cmd/ctx-breakdown -html out.html req.json  # write HTML to a path
//
// It auto-detects Anthropic vs OpenAI request shapes.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/contextaudit"
)

func main() {
	htmlPath := flag.String("html", "", "write an HTML report to this path (default /tmp/ctx-breakdown.html when -open is set)")
	open := flag.Bool("open", false, "open the HTML report in the browser ($BROWSER or firefox)")
	flag.Parse()

	data, srcName, err := readInput(flag.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, "ctx-breakdown:", err)
		os.Exit(2)
	}
	if len(data) == 0 {
		fmt.Fprintln(os.Stderr, "ctx-breakdown: no input (pass a file path or pipe JSON on stdin)")
		os.Exit(2)
	}

	report, format, err := contextaudit.FromRequestJSON(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ctx-breakdown:", err)
		os.Exit(1)
	}

	fmt.Printf("context breakdown — source: %s, detected format: %s\n", srcName, format)
	report.Render(os.Stdout)

	path := *htmlPath
	if path == "" && *open {
		path = filepath.Join(os.TempDir(), "ctx-breakdown.html")
	}
	if path != "" {
		title := fmt.Sprintf("ctx-breakdown — %s (%s)", srcName, format)
		if err := writeHTML(path, report, title); err != nil {
			fmt.Fprintln(os.Stderr, "ctx-breakdown: write html:", err)
			os.Exit(1)
		}
		fmt.Printf("\nHTML report: %s\n", path)
		if *open {
			if err := openInBrowser(path); err != nil {
				fmt.Fprintln(os.Stderr, "ctx-breakdown: open browser:", err)
			}
		}
	}
}

func readInput(args []string) ([]byte, string, error) {
	if len(args) > 0 && args[0] != "-" {
		data, err := os.ReadFile(args[0])
		if err != nil {
			return nil, "", fmt.Errorf("read %s: %w", args[0], err)
		}
		return data, args[0], nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, "", fmt.Errorf("read stdin: %w", err)
	}
	return data, "stdin", nil
}

func writeHTML(path string, report contextaudit.Report, title string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	report.RenderHTML(f, title)
	return nil
}

// openInBrowser launches $BROWSER (falling back to firefox) on path, detached.
func openInBrowser(path string) error {
	browser := os.Getenv("BROWSER")
	if browser == "" {
		browser = "firefox"
	}
	return exec.Command(browser, path).Start()
}
