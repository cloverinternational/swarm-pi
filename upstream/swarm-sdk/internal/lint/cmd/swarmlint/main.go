// Command swarmlint runs the Swarm SDK contract linter.
//
// Usage:
//
//	swarmlint [flags] package...
//
// Flags:
//
//	-ordered-blocks
//	      check for direct OrderedBlocks access (default true)
//	-message-construction
//	      check for direct Message{} construction (default true)
//	-custom-client
//	      check for custom JSON-RPC client usage (default true)
//	-documentation
//	      check for contract documentation (default false)
//
// Examples:
//
//	# Check current package
//	swarmlint .
//
//	# Check all packages
//	swarmlint ./...
//
//	# Check specific package with all checks
//	swarmlint -documentation ./pkg/...
//
//	# Run as a Go vet analyzer
//	go vet -vettool=$(which swarmlint) ./...
package main

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/lint"

	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(lint.Analyzer)
}
