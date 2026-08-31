package visual

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type JournalOp string

const (
	OpCreate      JournalOp = "create"
	OpResolve     JournalOp = "resolve"
	OpTimeout     JournalOp = "timeout"
	OpCancel      JournalOp = "cancel"
	OpAcknowledge JournalOp = "acknowledge"
	OpReviewPlan  JournalOp = "review_plan"
	OpSupersede   JournalOp = "supersede"
)

type journalEntry struct {
	Op          JournalOp `json:"op"`
	Screen      *Screen   `json:"screen,omitempty"`
	ID          string    `json:"id,omitempty"`
	Answer      []string  `json:"answer,omitempty"`
	UsedDefault bool      `json:"usedDefault,omitempty"`
	Decision    string    `json:"decision,omitempty"`
	SupersedeBy string    `json:"supersedeBy,omitempty"`
	T           int64     `json:"t"`
}

type Journal struct {
	mu   sync.Mutex
	path string
	f    *os.File
	enc  *json.Encoder
}

func OpenJournal(path string) (*Journal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir journal dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open journal: %w", err)
	}
	return &Journal{path: path, f: f, enc: json.NewEncoder(f)}, nil
}

func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}

func (j *Journal) append(e journalEntry) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	e.T = time.Now().Unix()
	return j.enc.Encode(e)
}

func (j *Journal) AppendCreate(s *Screen) error {
	if s.PrimitiveJSON == nil && s.Primitive != nil {
		raw, err := json.Marshal(s.Primitive)
		if err != nil {
			return fmt.Errorf("marshal primitive: %w", err)
		}
		s.PrimitiveJSON = raw
	}
	return j.append(journalEntry{Op: OpCreate, Screen: s})
}

func (j *Journal) AppendResolve(id string, answer []string, usedDefault bool) error {
	return j.append(journalEntry{Op: OpResolve, ID: id, Answer: answer, UsedDefault: usedDefault})
}

func (j *Journal) AppendTimeout(id string, answer []string, usedDefault bool) error {
	return j.append(journalEntry{Op: OpTimeout, ID: id, Answer: answer, UsedDefault: usedDefault})
}

func (j *Journal) AppendCancel(id string) error {
	return j.append(journalEntry{Op: OpCancel, ID: id})
}

func (j *Journal) AppendAck(id string) error {
	return j.append(journalEntry{Op: OpAcknowledge, ID: id})
}

func (j *Journal) AppendReviewPlan(id string, decision string) error {
	return j.append(journalEntry{Op: OpReviewPlan, ID: id, Decision: decision})
}

func (j *Journal) AppendSupersede(oldID, newID string) error {
	return j.append(journalEntry{Op: OpSupersede, ID: oldID, SupersedeBy: newID})
}

func Replay(path string) (map[string]*Screen, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*Screen{}, nil
		}
		return nil, err
	}
	defer f.Close()

	screens := map[string]*Screen{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var e journalEntry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		switch e.Op {
		case OpCreate:
			if e.Screen != nil {
				screens[e.Screen.ID] = e.Screen
			}
		case OpResolve:
			if s, ok := screens[e.ID]; ok {
				s.Kind = ScreenKindResolved
				s.Answer = e.Answer
				s.UsedDefault = e.UsedDefault
				now := time.Now()
				s.ResolvedAt = &now
			}
		case OpTimeout:
			if s, ok := screens[e.ID]; ok {
				s.Kind = ScreenKindTimedOut
				s.Answer = e.Answer
				s.UsedDefault = e.UsedDefault
				now := time.Now()
				s.ResolvedAt = &now
			}
		case OpCancel:
			if s, ok := screens[e.ID]; ok {
				s.Kind = ScreenKindCancelled
				now := time.Now()
				s.ResolvedAt = &now
			}
		case OpAcknowledge:
			if s, ok := screens[e.ID]; ok {
				s.Kind = ScreenKindAcknowledged
				now := time.Now()
				s.ResolvedAt = &now
			}
		case OpReviewPlan:
			if s, ok := screens[e.ID]; ok && s.Kind == ScreenKindPlan {
				switch e.Decision {
				case "approve":
					s.PlanState = PlanStateApproved
				case "reject":
					s.PlanState = PlanStateRejected
				case "abandon":
					s.PlanState = PlanStateAbandoned
				}
			}
		case OpSupersede:
			if s, ok := screens[e.ID]; ok && s.Kind == ScreenKindPlan {
				s.PlanState = PlanState("superseded_by:" + e.SupersedeBy)
			}
		}
	}
	return screens, scanner.Err()
}
