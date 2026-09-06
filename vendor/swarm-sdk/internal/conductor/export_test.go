// conductor_export_test.go — exports unexported functions for external tests.
// This file is compiled only during `go test`.
package conductor

import "time"

// ParseWorkflowForTest exposes the internal parseWorkflow function for
// external test packages.
func ParseWorkflowForTest(data []byte, absPath string) (*WorkflowDef, error) {
	return parseWorkflow(data, absPath)
}

// SortForDispatchForTest exposes the internal sortForDispatch function.
func SortForDispatchForTest(issues []Issue) []Issue {
	return sortForDispatch(issues)
}

// BackoffDelaysForTest returns a slice of computed backoff delays for attempts
// 1..n using the given WorkflowDef's MaxRetryBackoffMs setting.
func BackoffDelaysForTest(wf *WorkflowDef, n int) []time.Duration {
	o := &Orchestrator{workflow: wf}
	out := make([]time.Duration, n)
	for i := range n {
		out[i] = o.backoffDelay(i + 1)
	}
	return out
}
