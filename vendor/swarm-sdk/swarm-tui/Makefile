.PHONY: build install clean acp-server test-acp automation tools vhs vhs-check vhs-lineage vhs-baseline vhs-verify

BINARY_NAME := swarm
HEADLESS_BINARY := swarm-headless
ACP_SERVER_BINARY := swarmos-acp
# Resolve the SDK directory from this Makefile rather than Git metadata. Go's
# VCS stamping can fail when a checkout is shallow, copied without .git, or
# has damaged objects; the build should still locate the sibling headless
# package in those environments.
SWARM_SDK_ROOT := $(abspath $(CURDIR)/..)
GO_BUILD_FLAGS := -buildvcs=false
VHS := vhs
VHS_TAPE ?= manual_tests/vhs/lineage_fixture.tape
VHS_OUTPUT_DIR ?= manual_tests/vhs/output
VHS_BASELINE_DIR ?= manual_tests/vhs/baseline
INSTALL_DIR ?= /usr/local/bin

# ── Version ──────────────────────────────────────────────────────────────────
# BASE_VERSION is read directly from version.go — it is the single source of
# truth for the major.minor.patch number.  Bump it there; nowhere else.
BASE_VERSION := $(shell grep 'Version = ' ./internal/version/version.go | sed 's/.*Version = "\(.*\)"/\1/')
# Build number = total commit count.  Simple, monotonically increasing, no
# external tooling required.
BUILD_NUM    := $(shell git rev-list --count HEAD 2>/dev/null || echo 0)
# Full version shown in the binary.  Can be overridden: VERSION=v0.8.1 make install
VERSION      ?= $(BASE_VERSION)

GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
BUILD_ID := $(GIT_COMMIT)-$(shell date -u +"%Y%m%d%H%M%S")

# Package path for version variables
VERSION_PKG := github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version

# ldflags to inject version info at build time
LDFLAGS := -X '$(VERSION_PKG).Version=$(VERSION)' \
           -X '$(VERSION_PKG).GitCommit=$(GIT_COMMIT)' \
           -X '$(VERSION_PKG).BuildTime=$(BUILD_TIME)' \
           -X '$(VERSION_PKG).BuildID=$(BUILD_ID)' \
           -X '$(VERSION_PKG).BuildNum=$(BUILD_NUM)'

build:
	@echo "Building $(BINARY_NAME) $(VERSION) ($(BUILD_ID))"
	go build $(GO_BUILD_FLAGS) -tags fts5 -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) ./cmd/swarmos
	@echo "Build complete: $(BINARY_NAME)"
	@echo "Building $(HEADLESS_BINARY) $(VERSION) ($(BUILD_ID))"
	@# Build the headless binary into this Makefile's own directory so that
	@# `make install` (and scripts/install.sh) can find it next to $(BINARY_NAME).
	@# Use an absolute -o path because the build runs from $(SWARM_SDK_ROOT),
	@# whose position relative to the git root varies between the monorepo and
	@# standalone layouts — $(CURDIR) is always the swarm-tui source directory.
	cd $(SWARM_SDK_ROOT) && go build $(GO_BUILD_FLAGS) -o $(CURDIR)/$(HEADLESS_BINARY) ./cmd/headless
	@echo "Build complete: $(HEADLESS_BINARY)"

acp-server:
	@echo "Building $(ACP_SERVER_BINARY) $(VERSION) ($(BUILD_ID))"
	go build $(GO_BUILD_FLAGS) -tags fts5 -ldflags "$(LDFLAGS)" -o $(ACP_SERVER_BINARY) ./headless/cmd/acp-server
	@echo "Build complete: $(ACP_SERVER_BINARY)"

# Run the ACP protocol unit + integration tests.
test-acp:
	go test -v -count=1 ./headless/acp/...

# ── Install ───────────────────────────────────────────────────────────────────
# Installs to every location that matters so `swarm -version` always shows the
# right thing regardless of which directory is first in PATH.
#
#   1. $(INSTALL_DIR)  — system-wide, always updated (sudo if needed)
#   2. ~/bin           — user-local override, updated when the directory exists
#
install: build
	@BINARY_NAME="$(BINARY_NAME)" \
	HEADLESS_BINARY="$(HEADLESS_BINARY)" \
	VERSION="$(VERSION)" \
	BUILD_ID="$(BUILD_ID)" \
	./scripts/install.sh "$(INSTALL_DIR)"
	@$(MAKE) -C ../sac install INSTALL_DIR="$(INSTALL_DIR)"

clean:
	rm -f $(BINARY_NAME) $(HEADLESS_BINARY) $(ACP_SERVER_BINARY)
	go clean

# Show version info that will be injected
version-info:
	@echo "Base version: $(BASE_VERSION)  (from version.go)"
	@echo "Version:      $(VERSION)"
	@echo "Build Num:    $(BUILD_NUM)"
	@echo "Git Commit:   $(GIT_COMMIT)"
	@echo "Build Time:   $(BUILD_TIME)"
	@echo "Build ID:     $(BUILD_ID)"

# Build automation tools
automation:
	@echo "Building automation tools..."
	go build $(GO_BUILD_FLAGS) -tags fts5 -o tui-automation ./cmd/tui-automation/
	go build $(GO_BUILD_FLAGS) -tags fts5 -o tui-preview ./cmd/tui-preview/
	@echo "Built: tui-automation, tui-preview"

# Build all tools including automation
tools: build automation
	@echo "All tools built"

vhs-check:
	@command -v $(VHS) >/dev/null 2>&1 || (echo "vhs is required. Install with: brew install vhs" && exit 1)

# Record deterministic TUI visuals from the render sandbox.
vhs: build vhs-check
	@./scripts/vhs-record.sh $(VHS_TAPE)

# Record the lineage-focused fixture used during active implementation loops.
vhs-lineage: VHS_TAPE=manual_tests/vhs/lineage_fixture.tape
vhs-lineage: vhs

# Refresh approved baseline images after intentional UI updates.
vhs-baseline: vhs-lineage
	@mkdir -p $(VHS_BASELINE_DIR)
	@cp $(VHS_OUTPUT_DIR)/lineage_collapsed.png $(VHS_BASELINE_DIR)/lineage_collapsed.png
	@cp $(VHS_OUTPUT_DIR)/lineage_expanded.png $(VHS_BASELINE_DIR)/lineage_expanded.png
	@echo "Updated baseline images in $(VHS_BASELINE_DIR)"

# Fail if current UI snapshots drift from baseline.
vhs-verify: vhs-lineage
	@go run ./cmd/pngdiff $(VHS_BASELINE_DIR)/lineage_collapsed.png $(VHS_OUTPUT_DIR)/lineage_collapsed.png
	@go run ./cmd/pngdiff $(VHS_BASELINE_DIR)/lineage_expanded.png $(VHS_OUTPUT_DIR)/lineage_expanded.png
	@echo "VHS visual verification passed"
