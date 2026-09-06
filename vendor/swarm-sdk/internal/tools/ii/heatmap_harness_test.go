package ii

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolmetrics"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ---------------------------------------------------------------------------
// Per-tool heatmap harness.
//
// This is a MEASUREMENT harness, not a correctness test. It exercises each
// safe tool with representative workloads and prints a ranked cost table.
//
// It is skipped unless SWARM_HEATMAP=1 so it never runs in normal CI.
//
// Tool safety classification (why not every tool is driven here):
//
//	Class A  pure/read-only        -> driven here (Read, grep)
//	Class B  local side effects    -> driven here in a temp workspace
//	                                  (apply_patch, Undo, TaskManage,
//	                                  semantic_rename dry-run)
//	Class C  spawns agents/procs   -> NOT driven: Subagent/Delegate/
//	                                  BackgroundTask cost real LLM tokens and
//	                                  were the source of the 2.4GB spike.
//	                                  Measure from live data instead.
//	Class D  network/external      -> NOT driven: websearch, mcp_*, browser
//	                                  are non-deterministic.
//	Class E  interactive/blocking  -> NOT driven: ask_user_question,
//	                                  ask_parent, exit_plan_mode block on a
//	                                  human.
//	Class F  stateful/destructive  -> NEVER driven: annoyed publishes a real
//	                                  GitHub issue, vault_* touches secrets,
//	                                  cron_* schedules real jobs.
// ---------------------------------------------------------------------------

// scenario is one measured workload against one tool.
type scenario struct {
	tool   string
	label  string
	params map[string]any
	// iters overrides the default iteration count for expensive scenarios.
	iters int
}

const defaultIters = 20

// buildCorpus creates a workspace whose shape mirrors a real repo: many small
// files, some Go sources for AST search, and one large file. File sizes are
// the dominant input to read/search cost, so a flat corpus would understate
// the tail.
func buildCorpus(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// 200 small text files (~2KB each) across nested dirs.
	for i := 0; i < 200; i++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg%02d", i%10))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := strings.Repeat(fmt.Sprintf("line %d needle_rare alpha beta gamma\n", i), 40)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%03d.txt", i)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 20 Go files for symbol/AST search.
	for i := 0; i < 20; i++ {
		src := fmt.Sprintf(`package pkg%02d

import "fmt"

// Handler%02d processes requests.
type Handler%02d struct {
	Name  string
	Count int
}

func (h *Handler%02d) Execute(input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("empty")
	}
	h.Count++
	return h.Name + input, nil
}

func NewHandler%02d(name string) *Handler%02d {
	return &Handler%02d{Name: name}
}
`, i%10, i, i, i, i, i, i)
		dir := filepath.Join(root, fmt.Sprintf("pkg%02d", i%10))
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("h%02d.go", i)), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// One large file (~2MB) to expose read/scan cost at the tail.
	var big strings.Builder
	for i := 0; big.Len() < 2<<20; i++ {
		fmt.Fprintf(&big, "%06d the quick brown fox jumps over the lazy dog needle_rare\n", i)
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(big.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	// Target file for mutation scenarios.
	if err := os.WriteFile(filepath.Join(root, "mutable.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func scenarios(root string) []scenario {
	large := filepath.Join(root, "large.txt")
	small := filepath.Join(root, "pkg00", "f000.txt")
	goFile := filepath.Join(root, "pkg00", "h00.go")

	return []scenario{
		// ---- Read: the highest-frequency tool, 7 distinct cost profiles ----
		{tool: "Read", label: "read_small_4kb", params: map[string]any{"file_path": small}},
		{tool: "Read", label: "read_large_2mb", params: map[string]any{"file_path": large, "max_size": 64 << 20}, iters: 5},
		{tool: "Read", label: "read_range_100", params: map[string]any{"file_path": large, "start_line": 1000, "end_line": 1100, "max_size": 64 << 20}},
		{tool: "Read", label: "read_tail_20", params: map[string]any{"file_path": large, "tail": 20, "max_size": 64 << 20}},
		{tool: "Read", label: "read_head_50", params: map[string]any{"file_path": large, "head": 50, "max_size": 64 << 20}},
		{tool: "Read", label: "search_in_file", params: map[string]any{
			"mode": "search", "file_path": large, "pattern": "needle_rare"}, iters: 5},
		{tool: "Read", label: "search_in_dir", params: map[string]any{
			"mode": "search", "file_path": root, "pattern": "needle_rare", "file_pattern": "*.txt"}, iters: 5},
		{tool: "Read", label: "read_large_capped", params: map[string]any{"file_path": large}, iters: 5},
		{tool: "Read", label: "files_glob_go", params: map[string]any{
			"mode": "files", "file_path": root, "pattern": "*.go"}},

		// ---- grep: text modes vs AST modes ----
		{tool: "grep", label: "text_content", params: map[string]any{
			"pattern": "needle_rare", "path": root, "output_mode": "content"}, iters: 5},
		{tool: "grep", label: "text_files_only", params: map[string]any{
			"pattern": "needle_rare", "path": root, "output_mode": "files"}, iters: 5},
		{tool: "grep", label: "text_count", params: map[string]any{
			"pattern": "needle_rare", "path": root, "output_mode": "count"}, iters: 5},
		{tool: "grep", label: "ast_symbol", params: map[string]any{
			"pattern": "Handler", "path": root, "search_type": "symbol"}, iters: 5},
		{tool: "grep", label: "ast_document", params: map[string]any{
			"file_path": goFile, "search_type": "document"}, iters: 5},
		{tool: "grep", label: "ast_references", params: map[string]any{
			"pattern": "Execute", "path": root, "search_type": "references"}, iters: 5},

		// ---- TaskManage: in-memory, expected to be cheap ----
		{tool: "TaskManage", label: "create", params: map[string]any{
			"operations": []any{map[string]any{
				"key": "k1", "op": "create", "subject": "probe task", "category": "researching"}}}},
		{tool: "TaskManage", label: "list", params: map[string]any{
			"operations": []any{map[string]any{"key": "l1", "op": "list"}}}},

		// ---- semantic_rename: dry run only, never mutates ----
		{tool: "semantic_rename", label: "dry_run", params: map[string]any{
			"old_name": "Handler00", "new_name": "Handler99", "dry_run": true}, iters: 5},
	}
}

// allowAllChecker grants every permission. The harness measures execution
// cost, not policy; using the real interactive checker would block on prompts.
type allowAllChecker struct{}

func (allowAllChecker) Check(context.Context, []tools.Permission) bool { return true }
func (allowAllChecker) CheckWithContext(context.Context, []tools.Permission, map[string]any) bool {
	return true
}
func (allowAllChecker) RequestApproval(context.Context, []tools.Permission, string) bool { return true }
func (allowAllChecker) Grant(tools.Permission, tools.ToolScope, string) error            { return nil }
func (allowAllChecker) Revoke(tools.Permission, tools.ToolScope, string) error           { return nil }
func (allowAllChecker) IsGranted(tools.Permission, tools.ToolScope, string) bool         { return true }

// buildRegistry registers the ii tools into a SimpleRegistry so calls traverse
// SimpleRegistry.Execute -- the SAME path production uses, and the path the
// heatmap instrumentation hooks. Calling tool.Execute directly would bypass
// measuredExecute entirely and record nothing.
func buildRegistry(t *testing.T, root string) *tools.SimpleRegistry {
	t.Helper()
	tr, err := NewToolRegistry(root)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	reg := tools.NewSimpleRegistry(nil, nil)
	reg.SetPermissionChecker(allowAllChecker{})
	for _, tool := range tr.tools {
		if err := reg.Register(tool); err != nil {
			t.Fatalf("register %s: %v", tool.Name(), err)
		}
	}
	return reg
}

func TestHeatmapHarness(t *testing.T) {
	if os.Getenv("SWARM_HEATMAP") != "1" {
		t.Skip("set SWARM_HEATMAP=1 to run the measurement harness")
	}

	root := buildCorpus(t)
	reg := buildRegistry(t, root)

	ctx := context.Background()
	toolmetrics.Reset()

	type row struct {
		label   string
		tool    string
		iters   int
		wall    time.Duration
		bytes   int
		errText string
	}
	var rows []row

	for _, sc := range scenarios(root) {
		if !reg.IsRegistered(sc.tool) {
			rows = append(rows, row{label: sc.label, tool: sc.tool, errText: "TOOL NOT REGISTERED"})
			continue
		}
		iters := sc.iters
		if iters == 0 {
			iters = defaultIters
		}

		// Warm once so first-call index building is not billed to the mean.
		var warmErr string
		if res, err := reg.Execute(ctx, sc.tool, sc.params); err != nil {
			warmErr = "ERR: " + err.Error()
		} else if res != nil && res.IsError {
			warmErr = "IsError: " + truncate(res.Output, 120)
		} else if res != nil && strings.HasPrefix(strings.TrimSpace(res.Output), "ERROR:") {
			// Some tools report failure as plain output text with a nil error.
			// Without this the scenario would silently measure an error path
			// and be reported as "ok".
			warmErr = "SOFT-FAIL: " + truncate(res.Output, 120)
		}

		start := time.Now()
		var payload int
		for i := 0; i < iters; i++ {
			res, err := reg.Execute(ctx, sc.tool, sc.params)
			if err == nil {
				payload = tools.ResultPayloadSize(res)
			}
		}
		elapsed := time.Since(start)

		rows = append(rows, row{
			label:   sc.label,
			tool:    sc.tool,
			iters:   iters,
			wall:    elapsed / time.Duration(iters),
			bytes:   payload,
			errText: warmErr,
		})
	}

	fmt.Println()
	fmt.Println("=========== PER-SCENARIO COST (mean per call) ===========")
	fmt.Printf("%-16s %-18s %6s %12s %12s  %s\n", "TOOL", "SCENARIO", "ITERS", "MEAN", "PAYLOAD_B", "NOTE")
	for _, r := range rows {
		note := r.errText
		if note == "" {
			note = "ok"
		}
		fmt.Printf("%-16s %-18s %6d %12s %12d  %s\n",
			r.tool, r.label, r.iters, r.wall.Round(time.Microsecond), r.bytes, truncate(note, 90))
	}

	fmt.Println()
	fmt.Println("=========== AGGREGATE HEATMAP (ranked by total time) ===========")
	fmt.Printf("%-18s %8s %8s %12s %12s %12s %14s\n",
		"TOOL", "CALLS", "ERRORS", "TOTAL_MS", "MEAN_MS", "MAX_MS", "OUT_BYTES")
	for _, s := range toolmetrics.SnapshotAll() {
		fmt.Printf("%-18s %8d %8d %12.2f %12.3f %12.3f %14d\n",
			s.Tool, s.Calls, s.Errors, s.TotalMS, s.MeanMS, s.MaxMS, s.OutputBytes)
	}

	fmt.Println()
	fmt.Println("=========== LATENCY DISTRIBUTION ===========")
	for _, s := range toolmetrics.SnapshotAll() {
		fmt.Printf("%-18s %v\n", s.Tool, s.Buckets)
	}

	if toolmetrics.DeepProfileEnabled() {
		fmt.Println()
		fmt.Println("=========== ALLOCATION (deep profile) ===========")
		fmt.Printf("%-18s %14s %10s %14s\n", "TOOL", "ALLOC_BYTES", "SAMPLES", "MEAN_ALLOC")
		for _, s := range toolmetrics.SnapshotAll() {
			fmt.Printf("%-18s %14d %10d %14d\n", s.Tool, s.AllocBytes, s.AllocSamples, s.MeanAlloc)
		}
	}
	fmt.Println()
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
