// Tree-walking helpers for the auditable session-tree feature.
//
// These functions provide read-side access to the parent/child structure
// implied by Task.ParentID + Task.SiblingIndex, plus the constructors that
// keep that structure consistent (cycle-free, dense sibling indices,
// idempotent decomposition).
//
// All methods take the store mutex appropriately; callers do not need to
// lock externally.

package taskstore

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrCycleDetected is returned by AddTask / AddDecomposedSubtask /
// UpdateTask when the proposed ParentID would create a cycle in the
// hierarchy edge.
var ErrCycleDetected = fmt.Errorf("task: parent_id would create a cycle")

// ErrAlreadyDecomposed is returned by AddDecomposedSubtask helpers when
// the parent task already has a non-empty DecomposedBy set, signalling
// that decomposition has already happened (or is in flight) for that
// parent. This is the idempotency guard.
var ErrAlreadyDecomposed = fmt.Errorf("task: parent already decomposed")

// ChildrenOf returns the children of parentID, sorted by SiblingIndex
// ascending. An empty parentID returns the top-level tasks (those with
// no parent set).
func (s *Store) ChildrenOf(parentID string) []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.childrenOfLocked(parentID)
}

// childrenOfLocked must be called with s.mu held (read or write).
func (s *Store) childrenOfLocked(parentID string) []Task {
	out := make([]Task, 0, 4)
	for _, t := range s.Tasks {
		if t.ParentID == parentID && t.Status != StatusDeleted {
			out = append(out, t)
		}
	}
	// Stable order by SiblingIndex; fall back to CreatedAt for ties so
	// legacy tasks (SiblingIndex=0) still produce a deterministic order.
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && (out[j-1].SiblingIndex > out[j].SiblingIndex ||
			(out[j-1].SiblingIndex == out[j].SiblingIndex &&
				out[j-1].CreatedAt.After(out[j].CreatedAt))) {
			out[j-1], out[j] = out[j], out[j-1]
			j--
		}
	}
	return out
}

// AncestorChain returns the chain of tasks from taskID up to the root,
// inclusive. Index 0 is the task itself; the last entry is the top-level
// ancestor (ParentID == ""). Returns the chain even if a cycle exists,
// truncated at the first repeat — callers should pair this with a
// ValidateNoCycles check during writes, not reads.
func (s *Store) AncestorChain(taskID string) []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	chain := make([]Task, 0, 4)
	seen := make(map[string]struct{}, 4)
	cursor := taskID
	for cursor != "" {
		if _, dup := seen[cursor]; dup {
			break
		}
		seen[cursor] = struct{}{}

		t, ok := s.findTaskLocked(cursor)
		if !ok {
			break
		}
		chain = append(chain, t)
		cursor = t.ParentID
	}
	return chain
}

// DisplayNumber returns the dotted x.y.z label for taskID by walking the
// ancestor chain and indexing each ancestor by its SiblingIndex within
// its parent's child group. Indices are 1-based and dense (gaps are
// closed). Legacy tasks (no ParentID anywhere in the chain and
// SiblingIndex==0) get a flat ordinal.
//
//	root → "1"
//	root → child → "1.2"
//	root → child → grandchild → "1.2.1"
func (s *Store) DisplayNumber(taskID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	chain := s.ancestorChainLocked(taskID)
	if len(chain) == 0 {
		return ""
	}

	// Walk from root to leaf, computing each level's 1-based dense index
	// among its siblings.
	parts := make([]string, 0, len(chain))
	for i := len(chain) - 1; i >= 0; i-- {
		node := chain[i]
		idx := s.denseSiblingPositionLocked(node)
		if idx <= 0 {
			// Defensive: should never happen because the task itself is
			// in its parent's child set, but bail out gracefully.
			idx = 1
		}
		parts = append(parts, strconv.Itoa(idx))
	}
	return strings.Join(parts, ".")
}

// denseSiblingPositionLocked returns the 1-based position of t within
// its parent's child group, ignoring gaps in SiblingIndex. Must be
// called with s.mu held.
func (s *Store) denseSiblingPositionLocked(t Task) int {
	siblings := s.childrenOfLocked(t.ParentID)
	for i, sib := range siblings {
		if sib.ID == t.ID {
			return i + 1
		}
	}
	return 0
}

// ancestorChainLocked must be called with s.mu held.
func (s *Store) ancestorChainLocked(taskID string) []Task {
	chain := make([]Task, 0, 4)
	seen := make(map[string]struct{}, 4)
	cursor := taskID
	for cursor != "" {
		if _, dup := seen[cursor]; dup {
			break
		}
		seen[cursor] = struct{}{}

		t, ok := s.findTaskLocked(cursor)
		if !ok {
			break
		}
		chain = append(chain, t)
		cursor = t.ParentID
	}
	return chain
}

// findTaskLocked must be called with s.mu held.
func (s *Store) findTaskLocked(id string) (Task, bool) {
	for _, t := range s.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// validateNoCycleLocked walks proposedParentID up the existing parent
// chain and returns ErrCycleDetected if newTaskID is encountered.
// Must be called with s.mu held. An empty proposedParentID is always
// safe.
func (s *Store) validateNoCycleLocked(newTaskID, proposedParentID string) error {
	if proposedParentID == "" || newTaskID == "" {
		return nil
	}

	cursor := proposedParentID
	seen := make(map[string]struct{}, 4)
	for cursor != "" {
		if cursor == newTaskID {
			return ErrCycleDetected
		}
		if _, dup := seen[cursor]; dup {
			// Existing cycle in the store — a separate bug, but don't
			// loop forever here.
			return ErrCycleDetected
		}
		seen[cursor] = struct{}{}

		parent, ok := s.findTaskLocked(cursor)
		if !ok {
			// Unknown ancestor — we treat that as "no cycle from here"
			// rather than rejecting. AddTask validates parent existence
			// separately (callers can choose).
			return nil
		}
		cursor = parent.ParentID
	}
	return nil
}

// nextSiblingIndexLocked returns the next dense SiblingIndex to assign to
// a new child of parentID (max existing + 1, or 1 for the first child).
// Must be called with s.mu held.
func (s *Store) nextSiblingIndexLocked(parentID string) int {
	max := 0
	for _, t := range s.Tasks {
		if t.ParentID == parentID && t.Status != StatusDeleted {
			if t.SiblingIndex > max {
				max = t.SiblingIndex
			}
		}
	}
	return max + 1
}

// AddDecomposedSubtask appends child as a decomposition child of
// parentID. It atomically:
//
//   - Verifies the parent exists and is not itself deleted.
//   - Verifies adding child would not introduce a cycle.
//   - Verifies the parent has not already been decomposed (idempotency
//     guard via compare-and-swap on parent.DecomposedBy when claimParent
//     is non-empty). Pass claimParent="" to skip the CAS — useful when
//     the caller has already claimed via ClaimDecomposition.
//   - Inherits OriginPromptID + PlanID from parent.
//   - Assigns the next dense SiblingIndex.
//   - Sets child.ParentID = parentID and child.DecomposedBy =
//     "agent:<parentID>" if not already set.
//
// Returns the assigned child task (with timestamps and id-derived fields
// populated). The child argument should already have a unique ID.
func (s *Store) AddDecomposedSubtask(parentID string, child Task, claimParent string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Parent must exist and not be deleted.
	parentIdx := -1
	for i, t := range s.Tasks {
		if t.ID == parentID {
			parentIdx = i
			break
		}
	}
	if parentIdx < 0 {
		return Task{}, fmt.Errorf("task: parent %q not found", parentID)
	}
	parent := s.Tasks[parentIdx]
	if parent.Status == StatusDeleted {
		return Task{}, fmt.Errorf("task: parent %q is deleted", parentID)
	}

	// 2. CAS on parent.DecomposedBy when caller wants to claim atomically.
	if claimParent != "" {
		if parent.DecomposedBy != "" {
			return Task{}, ErrAlreadyDecomposed
		}
		s.Tasks[parentIdx].DecomposedBy = claimParent
		s.Tasks[parentIdx].DecompositionAttempts++
		s.Tasks[parentIdx].UpdatedAt = time.Now()
	}

	// 3. Cycle check.
	if err := s.validateNoCycleLocked(child.ID, parentID); err != nil {
		return Task{}, err
	}

	// 4. Inherit + populate.
	child.ParentID = parentID
	if child.OriginPromptID == "" {
		child.OriginPromptID = parent.OriginPromptID
	}
	if child.PlanID == "" {
		child.PlanID = parent.PlanID
	}
	if child.SiblingIndex == 0 {
		child.SiblingIndex = s.nextSiblingIndexLocked(parentID)
	}
	if child.DecomposedBy == "" {
		child.DecomposedBy = "agent:" + parentID
	}
	if child.CreatedAt.IsZero() {
		child.CreatedAt = time.Now()
	}
	child.UpdatedAt = time.Now()
	child.LastSeen = time.Now()

	// 5. Reject duplicate child ID.
	for _, t := range s.Tasks {
		if t.ID == child.ID {
			return Task{}, fmt.Errorf("task: child id %q already exists", child.ID)
		}
	}

	s.Tasks = append(s.Tasks, child)
	return child, nil
}

// ClaimDecomposition atomically reserves a parent task for a decomposer
// run. It sets parent.DecomposedBy to actor and increments
// DecompositionAttempts iff parent.DecomposedBy is currently empty.
// Returns ErrAlreadyDecomposed when the slot is already taken.
//
// This is the standalone CAS used when a host wants to mark "I am about
// to call my TaskDecomposer" before running an LLM call, separate from
// the eventual AddDecomposedSubtask insertions.
func (s *Store) ClaimDecomposition(parentID, actor string) error {
	if actor == "" {
		return fmt.Errorf("task: actor must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, t := range s.Tasks {
		if t.ID != parentID {
			continue
		}
		if t.Status == StatusDeleted {
			return fmt.Errorf("task: parent %q is deleted", parentID)
		}
		if t.DecomposedBy != "" {
			return ErrAlreadyDecomposed
		}
		s.Tasks[i].DecomposedBy = actor
		s.Tasks[i].DecompositionAttempts++
		s.Tasks[i].UpdatedAt = time.Now()
		return nil
	}
	return fmt.Errorf("task: parent %q not found", parentID)
}

// RecordDecompositionFailure clears the DecomposedBy claim on a parent
// (so the host can retry later) and records the error message on
// LastDecompositionError. Used when a TaskDecomposer Decompose() call
// returned a non-nil error after ClaimDecomposition succeeded.
func (s *Store) RecordDecompositionFailure(parentID, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, t := range s.Tasks {
		if t.ID != parentID {
			continue
		}
		s.Tasks[i].DecomposedBy = ""
		s.Tasks[i].LastDecompositionError = errMsg
		s.Tasks[i].UpdatedAt = time.Now()
		return nil
	}
	return fmt.Errorf("task: parent %q not found", parentID)
}
