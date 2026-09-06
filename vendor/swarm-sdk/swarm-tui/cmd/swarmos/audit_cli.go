// Package main — audit_cli.go
//
// `swarm audit` spins up a sandboxed *shallow copy* of a repository, asks it a
// set of questions, and audits exactly how the LLM context is assembled for
// each — emitting a single JSON document. It makes the normally-hidden pieces
// (ephemeral system blocks, injected swarmos_context, <system-reminder>
// nudges, task/skill reminders, hook output) explicit and quantified via the
// internal/contextaudit engine.
//
// Auto mode: when provider credentials are available it performs a REAL
// headless run per question and records true provider usage; otherwise it
// falls back to an offline assembly breakdown (estimated tokens). The
// assembly breakdown is ALWAYS emitted.
//
// Usage:
//
//	swarm audit                         # audit the current repo (auto)
//	swarm audit --repo /path/to/repo    # audit another repo
//	swarm audit --offline               # never call a provider
//	swarm audit --question "..."        # add a custom question (repeatable)
//	swarm audit --questions-file q.txt  # one question per line
//	swarm audit --output audit.json     # write JSON to a file
//	swarm audit request req.json        # break down a captured provider body
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	iagent "github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/contextaudit"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// defaultAuditQuestions are read-oriented prompts that exercise context
// assembly without depending on the repo's specifics or mutating anything.
var defaultAuditQuestions = []string{
	"What does this repository do? Summarize its purpose in a few sentences.",
	"List the main packages or top-level directories and their responsibilities.",
	"Where is the primary entrypoint and how is the project built?",
}

type capturedRequestBody struct {
	Body    []byte
	Ordinal int
	Count   int
}

func framedMarkerOffsets(s, marker string) []int {
	var offsets []int
	divider := strings.Repeat("═", 70)
	framedMarker := divider + "\n" + marker
	for offset := 0; offset < len(s); {
		rel := strings.Index(s[offset:], framedMarker)
		if rel < 0 {
			break
		}
		frameStart := offset + rel
		if frameStart == 0 || s[frameStart-1] == '\n' {
			offsets = append(offsets, frameStart+len(divider)+1)
		}
		offset = frameStart + len(framedMarker)
	}
	return offsets
}

// captureFinalRequestBody extracts the final request-body JSON object from the
// provider raw dump at path. ExecuteResponse.InputTokens describes the final
// usage-bearing provider call, so selecting the first request would pair two
// different calls whenever the agent used tools or retried.
//
// DebugTransport writes an exact 70-character frame divider before each
// "[REQUEST]" and "[RESPONSE]" banner. Requiring that signature prevents raw
// response or user content containing marker-like lines from becoming false
// transport boundaries. If any framed request is incomplete, capture fails
// closed: an earlier body must never inherit later usage.
func captureFinalRequestBody(path string) (capturedRequestBody, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return capturedRequestBody{}, false
	}
	s := string(data)

	markers := framedMarkerOffsets(s, "[REQUEST] ")
	if len(markers) == 0 {
		return capturedRequestBody{}, false
	}

	var bodies [][]byte
	for i, start := range markers {
		end := len(s)
		if i+1 < len(markers) {
			end = markers[i+1]
		}
		segment := s[start:end]
		bannerEnd := strings.IndexByte(segment, '\n')
		if bannerEnd < 0 {
			return capturedRequestBody{}, false
		}
		bodySection := segment[bannerEnd+1:]
		responses := framedMarkerOffsets(bodySection, "[RESPONSE] ")
		if i == len(markers)-1 && len(responses) == 0 {
			return capturedRequestBody{}, false
		}
		if len(responses) > 0 {
			bodySection = bodySection[:responses[0]]
		}
		brace := strings.IndexByte(bodySection, '{')
		if brace < 0 {
			return capturedRequestBody{}, false
		}
		dec := json.NewDecoder(strings.NewReader(bodySection[brace:]))
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return capturedRequestBody{}, false
		}
		bodies = append(bodies, append([]byte(nil), raw...))
	}

	return capturedRequestBody{
		Body:    bodies[len(bodies)-1],
		Ordinal: len(bodies),
		Count:   len(bodies),
	}, true
}

type auditDoc struct {
	Metadata  auditMeta      `json:"metadata"`
	Questions []auditEntry   `json:"questions"`
	Aggregate auditAggregate `json:"aggregate"`
}

type auditMeta struct {
	Tool            string   `json:"tool"`
	GeneratedAt     string   `json:"generated_at"`
	SourceRepo      string   `json:"source_repo"`
	SandboxPath     string   `json:"sandbox_path"`
	SandboxMethod   string   `json:"sandbox_method"`
	Mode            string   `json:"mode"`             // "real" or "offline"
	SandboxApproval string   `json:"sandbox_approval"` // tool-approval policy inside the sandbox ("yolo"/"readonly")
	SandboxWritable bool     `json:"sandbox_writable"` // true when the sandbox agent may edit files (apply_patch etc.)
	Provider        string   `json:"provider,omitempty"`
	Model           string   `json:"model,omitempty"`
	ContextWindow   int      `json:"context_window,omitempty"`
	SandboxKept     bool     `json:"sandbox_kept"`
	Notes           []string `json:"notes,omitempty"`
}

type auditEntry struct {
	Question    string                  `json:"question"`
	Breakdown   contextaudit.ReportJSON `json:"breakdown"`
	RealUsage   *auditUsage             `json:"real_usage,omitempty"`
	WireCapture *auditWireCapture       `json:"wire_capture,omitempty"`
	PctOfWindow float64                 `json:"pct_of_window,omitempty"`
	// RealBodyClassified is true when Breakdown was rebuilt from the ACTUAL
	// captured provider request body (so hidden/ephemeral injections are counted)
	// rather than estimated from just the user message.
	RealBodyClassified bool `json:"real_body_classified"`
}

type auditUsage struct {
	InputTokens     int     `json:"input_tokens"`
	InputRequestID  string  `json:"input_request_id,omitempty"`
	InputScope      string  `json:"input_scope"`
	OutputTokens    int     `json:"output_tokens"`
	OutputScope     string  `json:"output_scope"`
	TotalTokens     int     `json:"total_tokens"`
	TotalScope      string  `json:"total_scope"`
	TurnCount       int     `json:"turn_count"`
	CostUSD         float64 `json:"cost_usd"`
	ResponsePreview string  `json:"response_preview,omitempty"`
	ResponseFull    string  `json:"response_full,omitempty"`
}

type auditWireCapture struct {
	RequestID string `json:"request_id"`
	Ordinal   int    `json:"ordinal"`
	Count     int    `json:"count"`
	Pairing   string `json:"pairing"`
}

func auditRequestID(conversationID string, ordinal int) string {
	return fmt.Sprintf("%s/request-%d", conversationID, ordinal)
}

type auditAggregate struct {
	TotalQuestions   int      `json:"total_questions"`
	TotalEstTokens   int      `json:"total_estimated_tokens"`
	HiddenEstTokens  int      `json:"hidden_estimated_tokens"`
	HiddenPctOfTotal float64  `json:"hidden_pct_of_total"`
	HiddenSources    []string `json:"hidden_sources"`
	RealInputTokens  int      `json:"real_input_tokens,omitempty"`
	RealOutputTokens int      `json:"real_output_tokens,omitempty"`
}

// runAuditCLI is the `swarm audit` entrypoint.
func runAuditCLI(args []string) error {
	// Secondary mode: analyze a captured provider request body → JSON.
	if len(args) > 0 && args[0] == "request" {
		return runAuditRequest(args[1:])
	}

	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	repo := fs.String("repo", "", "repository to audit (default: current directory)")
	offline := fs.Bool("offline", false, "never call a provider; assemble context only")
	keep := fs.Bool("keep", false, "keep the sandbox copy instead of deleting it")
	pretty := fs.Bool("pretty", true, "pretty-print the JSON output")
	turns := fs.Int("turns", 1, "max turns per question in real mode")
	output := fs.String("output", "", "write JSON here (default: stdout)")
	providerFlag := fs.String("provider", "", "provider override (e.g. anthropic, openai)")
	modelFlag := fs.String("model", "", "model override")
	readonly := fs.Bool("readonly", false, "run the sandbox agent read-only (deny apply_patch/bash/writes); default allows edits")
	var extraQuestions multiFlag
	fs.Var(&extraQuestions, "question", "add an audit question (repeatable)")
	questionsFile := fs.String("questions-file", "", "file with one question per line")
	if err := fs.Parse(args); err != nil {
		return err
	}

	src := *repo
	if src == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve cwd: %w", err)
		}
		src = cwd
	}
	src, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("resolve repo path: %w", err)
	}
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return fmt.Errorf("repo %q is not a directory", src)
	}

	questions := append([]string{}, defaultAuditQuestions...)
	if *questionsFile != "" {
		fromFile, err := readQuestionsFile(*questionsFile)
		if err != nil {
			return err
		}
		questions = fromFile // a questions file fully replaces the defaults
	}
	questions = append(questions, extraQuestions...)

	// ── Sandbox: shallow copy of the repo ────────────────────────────────────
	sandbox, method, sandboxNote, cleanup, err := makeSandbox(src)
	if err != nil {
		return fmt.Errorf("create sandbox: %w", err)
	}
	if *keep {
		fmt.Fprintf(os.Stderr, "swarm audit: sandbox kept at %s\n", sandbox)
	} else {
		defer cleanup()
	}

	var notes []string
	if sandboxNote != "" {
		notes = append(notes, sandboxNote)
	}

	// ── Build a headless client rooted at the sandbox ────────────────────────
	resolvedProvider, resolvedModel := activeProfileMainModel()
	if *providerFlag != "" {
		resolvedProvider = *providerFlag
	}
	if *modelFlag != "" {
		resolvedModel = *modelFlag
	}
	storageDir := filepath.Join(sandbox, ".swarm-audit", "conversations")
	_ = os.MkdirAll(storageDir, 0o755)

	// ── Real wire-body capture (closes the "hidden bucket = 0" gap) ───────────
	// In real mode we ask the provider to dump its raw HTTP request body so we
	// can classify the ACTUAL assembled context — including the ephemeral
	// <system-reminder>/nudge/hook injections that only exist at runtime. The
	// anthropic/ClaudeCode provider honours SAC_RAW_DUMP. Give this run a private
	// file so stale or concurrent process output cannot be paired with our usage.
	// This must be set BEFORE the client/provider transport is constructed.
	captureEnabled := !*offline
	rawDumpPath := ""
	if captureEnabled {
		dumpDir, derr := os.MkdirTemp("", "swarm-audit-raw-")
		if derr != nil {
			captureEnabled = false
			notes = append(notes, fmt.Sprintf("raw request capture disabled: create private dump directory: %v", derr))
		} else {
			defer os.RemoveAll(dumpDir)
			rawDumpPath = filepath.Join(dumpDir, "provider.log")
			f, ferr := os.OpenFile(rawDumpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if ferr != nil {
				captureEnabled = false
				notes = append(notes, fmt.Sprintf("raw request capture disabled: create private dump file: %v", ferr))
			} else {
				_ = f.Close()
				prevDump, hadPrevDump := os.LookupEnv("SAC_RAW_DUMP")
				if serr := os.Setenv("SAC_RAW_DUMP", rawDumpPath); serr != nil {
					captureEnabled = false
					notes = append(notes, fmt.Sprintf("raw request capture disabled: configure provider dump path: %v", serr))
				} else {
					defer func() {
						if hadPrevDump {
							_ = os.Setenv("SAC_RAW_DUMP", prevDump)
						} else {
							_ = os.Unsetenv("SAC_RAW_DUMP")
						}
					}()
				}
			}
		}
	}

	// The sandbox is a throwaway shallow copy, so by default we let the agent
	// actually DO work — edit files (apply_patch), run bash, etc. — via the YOLO
	// approval checker. Without this the write tools declare RequiresPermission()
	// and, with no approval channel in a headless run, are denied
	// (tool.permission_denied). --readonly opts back into the deny-writes policy.
	sandboxApproval := "yolo"
	if *readonly {
		sandboxApproval = "readonly"
	}

	clientOpts := []sdkclient.Option{
		sdkclient.WithClientType(sdkclient.ClientTypeHeadless),
		sdkclient.WithWorkspace(sandbox),
		sdkclient.WithStorageDir(storageDir),
		sdkclient.WithApprovalMode(sandboxApproval),
	}
	if resolvedProvider != "" && resolvedModel != "" {
		clientOpts = append(clientOpts, sdkclient.WithProvider(sdkclient.Provider(resolvedProvider), resolvedModel))
	} else if resolvedProvider != "" {
		clientOpts = append(clientOpts, sdkclient.WithProvider(sdkclient.Provider(resolvedProvider), ""))
	}

	llm, err := sdkclient.New(clientOpts...)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	defer llm.Stop(context.Background())

	info := llm.ProviderInfo()
	format := providerFormat(info.Name)

	// Assemble the *real* system prompt and tool schemas the agent would send.
	systemPrompt := ""
	if def := llm.AgentInfo(); def != nil {
		systemPrompt = def.SystemPrompt
		if strings.TrimSpace(systemPrompt) == "" {
			if rendered, rerr := def.RenderSystemPrompt(); rerr == nil {
				systemPrompt = rendered
			}
		}
	}
	tools := toolsFromClient(llm)

	mode := "real"
	if *offline {
		mode = "offline"
		notes = append(notes, "offline forced via --offline; token counts are heuristic estimates")
	}

	ctx := context.Background()
	doc := auditDoc{
		Metadata: auditMeta{
			Tool:            "swarm audit",
			GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
			SourceRepo:      src,
			SandboxPath:     sandbox,
			SandboxMethod:   method,
			SandboxApproval: sandboxApproval,
			SandboxWritable: !*readonly,
			Provider:        info.Name,
			Model:           info.Model,
			ContextWindow:   info.ContextWindow,
			SandboxKept:     *keep,
		},
	}
	if *readonly {
		notes = append(notes, "sandbox is read-only (--readonly): apply_patch/bash/writes denied")
	} else {
		notes = append(notes, "sandbox is writable: apply_patch/bash/writes auto-approved (yolo) inside the disposable copy")
	}

	var aggEst, aggHidden, realIn, realOut int
	hiddenSources := map[string]bool{}

	for i, q := range questions {
		msgs := []contextaudit.Msg{{Role: "user", Content: q}}
		breakdown := contextaudit.BuildReportJSON(systemPrompt, tools, msgs, format)

		entry := auditEntry{Question: q, Breakdown: breakdown}
		if info.ContextWindow > 0 {
			entry.PctOfWindow = 100 * float64(breakdown.GrandTotalTokens) / float64(info.ContextWindow)
		}

		if mode == "real" {
			// Isolate this question's captured wire body from prior turns.
			captureThisQuestion := captureEnabled
			if captureThisQuestion {
				if terr := os.Truncate(rawDumpPath, 0); terr != nil {
					captureThisQuestion = false
					notes = append(notes, fmt.Sprintf("raw request capture disabled for question %d: truncate private dump: %v", i+1, terr))
				}
			}
			conversationID := fmt.Sprintf("audit-%d", i)
			resp, execErr := llm.Execute(ctx, iagent.ExecuteRequest{
				Message:        q,
				ConversationID: conversationID,
				MaxTurns:       *turns,
				StoragePath:    storageDir,
			})
			if execErr != nil {
				// Degrade to offline for the rest — most likely missing creds.
				mode = "offline"
				notes = append(notes, fmt.Sprintf("real run failed on question %d (%v); continued offline with estimated tokens", i+1, execErr))
			} else if resp != nil {
				entry.RealUsage = &auditUsage{
					InputTokens:     resp.InputTokens,
					InputScope:      "final_provider_request",
					OutputTokens:    resp.OutputTokens,
					OutputScope:     "cumulative_agent_execution",
					TotalTokens:     resp.TokensUsed,
					TotalScope:      "final_input_plus_cumulative_output",
					TurnCount:       resp.TurnCount,
					CostUSD:         resp.CostUSD,
					ResponsePreview: truncateAudit(resp.Message, 240),
					ResponseFull:    resp.Message,
				}
				if info.ContextWindow > 0 && resp.InputTokens > 0 {
					entry.PctOfWindow = 100 * float64(resp.InputTokens) / float64(info.ContextWindow)
				}
				realIn += resp.InputTokens
				realOut += resp.OutputTokens

				// Rebuild the breakdown from the ACTUAL captured request body so
				// the hidden/ephemeral injections are counted, not estimated.
				if captureThisQuestion {
					if capture, ok := captureFinalRequestBody(rawDumpPath); ok {
						if _, repJSON, fmt2, perr := contextaudit.FromRequestJSONFull(capture.Body); perr == nil {
							requestID := auditRequestID(conversationID, capture.Ordinal)
							repJSON.Format = fmt2
							repJSON.Estimate = true
							entry.Breakdown = repJSON
							entry.RealBodyClassified = true
							entry.WireCapture = &auditWireCapture{
								RequestID: requestID,
								Ordinal:   capture.Ordinal,
								Count:     capture.Count,
								Pairing:   "final_request_body_to_final_input_usage",
							}
							entry.RealUsage.InputRequestID = requestID
							breakdown = repJSON
							if info.ContextWindow > 0 && resp.InputTokens == 0 && repJSON.GrandTotalTokens > 0 {
								entry.PctOfWindow = 100 * float64(repJSON.GrandTotalTokens) / float64(info.ContextWindow)
							}
						}
					} else if i == 0 {
						notes = append(notes, "could not capture a raw provider request body (non-anthropic provider or dump disabled); breakdown is estimated from the user message only")
					}
				}
			}
		}

		aggEst += breakdown.GrandTotalTokens
		aggHidden += breakdown.Hidden.Tokens
		for _, s := range breakdown.Hidden.Sources {
			hiddenSources[s] = true
		}
		doc.Questions = append(doc.Questions, entry)
	}

	doc.Metadata.Mode = mode
	doc.Metadata.Notes = notes
	doc.Aggregate = auditAggregate{
		TotalQuestions:   len(questions),
		TotalEstTokens:   aggEst,
		HiddenEstTokens:  aggHidden,
		HiddenSources:    sortedKeys(hiddenSources),
		RealInputTokens:  realIn,
		RealOutputTokens: realOut,
	}
	if aggEst > 0 {
		doc.Aggregate.HiddenPctOfTotal = 100 * float64(aggHidden) / float64(aggEst)
	}

	return writeAuditJSON(doc, *output, *pretty)
}

// runAuditRequest analyzes a captured provider request body (as dumped by
// `swarmos --raw`) and emits the JSON breakdown.
func runAuditRequest(args []string) error {
	fs := flag.NewFlagSet("audit request", flag.ContinueOnError)
	pretty := fs.Bool("pretty", true, "pretty-print the JSON output")
	output := fs.String("output", "", "write JSON here (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	var data []byte
	var err error
	if len(rest) > 0 && rest[0] != "-" {
		data, err = os.ReadFile(rest[0])
	} else {
		data, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	_, repJSON, format, perr := contextaudit.FromRequestJSONFull(data)
	if perr != nil {
		return fmt.Errorf("parse request body: %w", perr)
	}
	repJSON.Format = format
	repJSON.Estimate = true
	b, err := repJSON.JSON(*pretty)
	if err != nil {
		return err
	}
	return writeBytes(b, *output)
}

// ── sandbox helpers ──────────────────────────────────────────────────────────

// makeSandbox creates a throwaway shallow copy of src. It prefers a shallow git
// clone (tracked files, no history); if src is not a git repo (or clone fails)
// it falls back to a filtered directory copy.
//
// note carries a human-readable reason when the preferred git-shallow-clone path
// was skipped or failed, so `swarm audit` can surface which method actually ran
// (and why) instead of silently degrading.
func makeSandbox(src string) (path, method, note string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "swarm-audit-")
	if err != nil {
		return "", "", "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }
	dst := filepath.Join(tmp, "repo")

	if isGitRepo(src) {
		cmd := exec.Command("git", "clone", "--depth", "1", "file://"+src, dst)
		if out, cerr := cmd.CombinedOutput(); cerr == nil {
			return dst, "git-shallow-clone", "", cleanup, nil
		} else {
			// fall through to copy; surface the reason via the note channel
			note = fmt.Sprintf("git shallow-clone failed (%v); fell back to filtered-copy: %s",
				cerr, strings.TrimSpace(truncateAudit(string(out), 200)))
		}
	} else {
		note = "source is not a git repository; used filtered-copy sandbox"
	}
	if cerr := copyTreeFiltered(src, dst); cerr != nil {
		cleanup()
		return "", "", "", nil, cerr
	}
	return dst, "filtered-copy", note, cleanup, nil
}

func isGitRepo(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (fi.IsDir() || fi.Mode().IsRegular())
}

// copyTreeFiltered copies src to dst skipping heavy/irrelevant directories and
// large binaries so the sandbox stays small and cheap.
func copyTreeFiltered(src, dst string) error {
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "vendor": true, ".worktrees": true,
		"dist": true, "build": true, "target": true, ".venv": true, "__pycache__": true,
	}
	const maxFileBytes = 1 << 20 // 1 MiB
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil // best-effort: skip unreadable entries
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return nil
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if fi.IsDir() {
			if skipDirs[fi.Name()] {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !fi.Mode().IsRegular() || fi.Size() > maxFileBytes {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
}

// ── small helpers ────────────────────────────────────────────────────────────

// multiFlag collects repeated string flags.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func readQuestionsFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read questions file: %w", err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if s := strings.TrimSpace(line); s != "" && !strings.HasPrefix(s, "#") {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("questions file %q has no questions", path)
	}
	return out, nil
}

func toolsFromClient(llm *sdkclient.Client) []provider.Tool {
	reg := llm.AgentToolRegistry()
	if reg == nil {
		return nil
	}
	names := reg.List()
	out := make([]provider.Tool, 0, len(names))
	for _, n := range names {
		t, err := reg.Get(n)
		if err != nil || t == nil {
			continue
		}
		out = append(out, provider.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	return out
}

func providerFormat(name string) string {
	if strings.Contains(strings.ToLower(name), "anthropic") {
		return "anthropic"
	}
	return "openai"
}

func truncateAudit(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func writeAuditJSON(doc auditDoc, output string, pretty bool) error {
	var b []byte
	var err error
	if pretty {
		b, err = json.MarshalIndent(doc, "", "  ")
	} else {
		b, err = json.Marshal(doc)
	}
	if err != nil {
		return err
	}
	return writeBytes(b, output)
}

func writeBytes(b []byte, output string) error {
	if output == "" {
		_, err := os.Stdout.Write(append(b, '\n'))
		return err
	}
	return os.WriteFile(output, append(b, '\n'), 0o644)
}
