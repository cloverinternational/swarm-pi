package observability

import (
	"fmt"
	"sort"
	"sync"
)

// LineageNode is one node in an error causality tree.
type LineageNode struct {
	ErrorID       string         `json:"error_id"`
	ParentErrorID string         `json:"parent_error_id,omitempty"`
	Code          string         `json:"code,omitempty"`
	Message       string         `json:"message,omitempty"`
	Operation     string         `json:"operation,omitempty"`
	Component     string         `json:"component,omitempty"`
	Children      []*LineageNode `json:"children,omitempty"`
}

// LineageReport is the debug lookup result for an ErrorID.
type LineageReport struct {
	ErrorID           string       `json:"error_id"`
	TraceID           string       `json:"trace_id"`
	OperationSequence []string     `json:"operation_sequence"`
	Events            []TraceEvent `json:"events"`
	CausalityTree     *LineageNode `json:"causality_tree,omitempty"`
}

// InMemoryLookupStore indexes trace events for ErrorID lookup.
type InMemoryLookupStore struct {
	mu           sync.RWMutex
	byTrace      map[string][]TraceEvent
	errorToTrace map[string]string
}

// NewInMemoryLookupStore creates an empty in-memory lookup index.
func NewInMemoryLookupStore() *InMemoryLookupStore {
	return &InMemoryLookupStore{
		byTrace:      make(map[string][]TraceEvent),
		errorToTrace: make(map[string]string),
	}
}

// StoreEvent adds one event to the in-memory index.
func (s *InMemoryLookupStore) StoreEvent(event TraceEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	traceID := event.TraceID
	if traceID != "" {
		s.byTrace[traceID] = append(s.byTrace[traceID], event)
	}
	if event.ErrorID != "" && traceID != "" {
		s.errorToTrace[event.ErrorID] = traceID
	}

	return nil
}

// LookupError returns the full lineage report for an ErrorID.
func (s *InMemoryLookupStore) LookupError(errorID string) (*LineageReport, error) {
	if errorID == "" {
		return nil, fmt.Errorf("error ID is required")
	}

	s.mu.RLock()
	traceID, ok := s.errorToTrace[errorID]
	if !ok {
		s.mu.RUnlock()
		return nil, fmt.Errorf("error ID %s not found", errorID)
	}
	events := append([]TraceEvent(nil), s.byTrace[traceID]...)
	s.mu.RUnlock()

	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Sequence != events[j].Sequence {
			return events[i].Sequence < events[j].Sequence
		}
		return events[i].Timestamp.Before(events[j].Timestamp)
	})

	sequence := buildOperationSequence(events)
	causality := buildCausalityTree(errorID, events)

	return &LineageReport{
		ErrorID:           errorID,
		TraceID:           traceID,
		OperationSequence: sequence,
		Events:            events,
		CausalityTree:     causality,
	}, nil
}

// LookupByErrorID is a convenience API for retrieving a lineage report.
func LookupByErrorID(store LookupStore, errorID string) (*LineageReport, error) {
	if store == nil {
		return nil, fmt.Errorf("lookup store is required")
	}
	return store.LookupError(errorID)
}

func buildOperationSequence(events []TraceEvent) []string {
	sequence := make([]string, 0, len(events))
	for _, event := range events {
		if event.Operation == "" {
			continue
		}
		if len(sequence) > 0 && sequence[len(sequence)-1] == event.Operation {
			continue
		}
		sequence = append(sequence, event.Operation)
	}
	return sequence
}

func buildCausalityTree(requestedErrorID string, events []TraceEvent) *LineageNode {
	nodes := make(map[string]*LineageNode)

	for _, event := range events {
		if event.ErrorID == "" {
			continue
		}
		node, ok := nodes[event.ErrorID]
		if !ok {
			node = &LineageNode{ErrorID: event.ErrorID}
			nodes[event.ErrorID] = node
		}
		if event.ParentErrorID != "" {
			node.ParentErrorID = event.ParentErrorID
		}
		if node.Operation == "" {
			node.Operation = event.Operation
		}
		if node.Component == "" {
			node.Component = event.Component
		}
		if node.Message == "" && event.Message != "" {
			node.Message = event.Message
		}
		if node.Code == "" {
			if code, ok := event.Attributes["error_code"].(string); ok {
				node.Code = code
			}
		}
	}

	for _, node := range nodes {
		if node.ParentErrorID == "" {
			continue
		}
		parent, ok := nodes[node.ParentErrorID]
		if !ok {
			parent = &LineageNode{ErrorID: node.ParentErrorID}
			nodes[node.ParentErrorID] = parent
		}
		parent.Children = append(parent.Children, node)
	}

	root, ok := nodes[requestedErrorID]
	if !ok {
		return nil
	}
	for root.ParentErrorID != "" {
		parent, exists := nodes[root.ParentErrorID]
		if !exists {
			break
		}
		root = parent
	}

	return root
}
