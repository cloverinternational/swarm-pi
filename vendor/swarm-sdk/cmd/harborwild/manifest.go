package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Manifest describes one real, already-fixed bug to package as a Harbor task.
// Everything in it is either a fact recoverable from git (SHA) or a decision a
// human author makes once per bug (title, instruction, which test(s) prove
// it). Nothing here is invented data about the bug itself.
type Manifest struct {
	// Slug is the task's identity, e.g. "wild/bench-ledger-silent-loss". Also
	// used as the Harbor task name in task.toml. Written under
	// <out>/<basename(Slug)>/.
	Slug string `json:"slug"`

	// SHA is the fix commit. Its first parent is used as the pre-fix
	// environment state.
	SHA string `json:"sha"`

	Title string `json:"title"`

	// InstructionFile is a path (relative to the manifest file's own
	// directory) to hand-authored Markdown describing the bug the way a real
	// user or reviewer would observe it -- symptoms and repro steps, not the
	// internal root cause file/line, so solving the task requires actually
	// finding the bug rather than pattern-matching an instruction that names
	// it.
	InstructionFile string `json:"instructionFile"`

	// GoModDir is the directory (relative to repo root) containing the go.mod
	// that owns TestPkg, e.g. "swarm-sdk".
	GoModDir string `json:"goModDir"`

	// TestPkg is the Go package pattern to test, e.g. "./internal/bench/...".
	TestPkg string `json:"testPkg"`

	// TestRun is the `go test -run` regex selecting the regression test(s)
	// that must go from FAIL (pre-fix) to PASS (post-fix). Required to be
	// specific enough that unrelated package tests don't dominate the signal,
	// but this tool also runs `go build` across GoModDir as a basic sanity
	// gate independent of TestRun.
	TestRun string `json:"testRun"`

	// ExtraTestFiles lists additional *_test.go paths (relative to repo root)
	// that must move from the parent commit to the fix commit's version even
	// though they are not part of the fix's own diff -- e.g. shared test
	// helpers the regression test depends on that were modified in a
	// different commit. Usually empty; most fixes are self-contained.
	ExtraTestFiles []string `json:"extraTestFiles,omitempty"`

	// BuildTags are Go build tags required, e.g. "fts5".
	BuildTags string `json:"buildTags,omitempty"`

	// NetworkMode is Harbor's environment.network_mode. Valid values per
	// harbor.models.task.config.NetworkMode: "no-network", "public", "allowlist".
	// Default "no-network" -- the archived repo tree plus the Go module cache
	// populated at image-build time should make the task fully offline for
	// the agent; only the image build itself needs "public" temporarily via a
	// separate build-time concern, not this field (Harbor's build_timeout_sec
	// covers image build; network_mode covers the agent's run-time network).
	NetworkMode string `json:"networkMode,omitempty"`
}

func loadManifest(path string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(path)
	if err != nil {
		return m, fmt.Errorf("read manifest: %w", err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("parse manifest %s: %w", path, err)
	}

	var missing []string
	if m.Slug == "" {
		missing = append(missing, "slug")
	}
	if m.SHA == "" {
		missing = append(missing, "sha")
	}
	if m.Title == "" {
		missing = append(missing, "title")
	}
	if m.InstructionFile == "" {
		missing = append(missing, "instructionFile")
	}
	if m.GoModDir == "" {
		missing = append(missing, "goModDir")
	}
	if m.TestPkg == "" {
		missing = append(missing, "testPkg")
	}
	if m.TestRun == "" {
		missing = append(missing, "testRun")
	}
	if len(missing) > 0 {
		return m, fmt.Errorf("manifest %s missing required field(s): %s", path, strings.Join(missing, ", "))
	}

	if m.NetworkMode == "" {
		m.NetworkMode = "no-network"
	}

	// Resolve InstructionFile relative to the manifest's own directory so
	// manifests are relocatable as a unit with their instruction text.
	if !filepath.IsAbs(m.InstructionFile) {
		m.InstructionFile = filepath.Join(filepath.Dir(path), m.InstructionFile)
	}
	if _, err := os.Stat(m.InstructionFile); err != nil {
		return m, fmt.Errorf("manifest %s: instructionFile %s: %w", path, m.InstructionFile, err)
	}

	return m, nil
}
