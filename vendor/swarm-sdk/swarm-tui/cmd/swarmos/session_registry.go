package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	sessionStateRunning     = "running"
	sessionStateInterrupted = "interrupted"
	sessionStateStopped     = "stopped"
	sessionStateCompleted   = "completed"
	sessionStateUnknown     = "unknown"
	sessionIntentRunning    = "running"
	sessionIntentStopped    = "stopped"
)

// TUISessionRecord is durable recovery intent plus corroborating process
// identity. PID alone is never trusted because the operating system may reuse
// it after a crash.
type TUISessionRecord struct {
	Version       int       `json:"version"`
	ID            string    `json:"id"`
	State         string    `json:"state"`
	Intent        string    `json:"intent"`
	Workspace     string    `json:"workspace"`
	ProjectRoot   string    `json:"project_root,omitempty"`
	Conversation  string    `json:"conversation_id,omitempty"`
	Executable    string    `json:"executable"`
	Args          []string  `json:"args,omitempty"`
	Handle        string    `json:"handle,omitempty"`
	ControlSocket string    `json:"control_socket,omitempty"`
	PID           int       `json:"pid,omitempty"`
	ProcessStart  string    `json:"process_start,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	LastError     string    `json:"last_error,omitempty"`
	Events        []string  `json:"events,omitempty"`
}

func sessionRegistryDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	dir := filepath.Join(home, ".swarmos", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create session registry: %w", err)
	}
	_ = os.Chmod(dir, 0o700)
	return dir, nil
}

func newSessionID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "tui-" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("tui-%d", time.Now().UnixNano())
}

func sessionPath(id string) (string, error) {
	if strings.TrimSpace(id) == "" || strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("invalid session id %q", id)
	}
	dir, err := sessionRegistryDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".json"), nil
}

func saveSessionRecord(record TUISessionRecord) error {
	if record.Version == 0 {
		record.Version = 1
	}
	record.UpdatedAt = time.Now().UTC()
	path, err := sessionPath(record.ID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session record: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("create session temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write session record: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync session record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install session record: %w", err)
	}
	return nil
}

func loadSessionRecord(id string) (TUISessionRecord, error) {
	path, err := sessionPath(id)
	if err != nil {
		return TUISessionRecord{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return TUISessionRecord{}, err
	}
	var record TUISessionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return TUISessionRecord{}, fmt.Errorf("decode session record %q: %w", id, err)
	}
	if record.Version != 1 || record.ID != id || record.Workspace == "" || record.Executable == "" {
		return TUISessionRecord{}, fmt.Errorf("invalid session record %q", id)
	}
	return record, nil
}

func listSessionRecords() ([]TUISessionRecord, error) {
	dir, err := sessionRegistryDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	records := make([]TUISessionRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		record, err := loadSessionRecord(id)
		if err != nil {
			records = append(records, TUISessionRecord{
				ID: id, State: sessionStateUnknown, LastError: err.Error(),
			})
			continue
		}
		reconcileSessionRecord(&record)
		_ = saveSessionRecord(record)
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].UpdatedAt.After(records[j].UpdatedAt)
	})
	return records, nil
}

func reconcileSessionRecord(record *TUISessionRecord) {
	if record.State != sessionStateRunning || record.PID <= 0 || record.ProcessStart == "" {
		return
	}
	start, executable, _, err := processEvidenceFunc(record.PID)
	if err == nil && start == record.ProcessStart && filepath.Clean(executable) == filepath.Clean(record.Executable) {
		return
	}
	record.State = sessionStateInterrupted
	record.LastError = "process is no longer the recorded instance"
	record.Events = append(record.Events, "marked interrupted during reconciliation")
}

func sessionProcessIdentity(record TUISessionRecord) (string, error) {
	start, executable, _, err := processEvidenceFunc(record.PID)
	if err != nil {
		return "", err
	}
	if start != record.ProcessStart || filepath.Clean(executable) != filepath.Clean(record.Executable) {
		return "", errors.New("process identity does not match session record")
	}
	return start, nil
}

func resumeSession(record TUISessionRecord) error {
	reconcileSessionRecord(&record)
	if record.State == sessionStateRunning {
		return fmt.Errorf("session %q is already running", record.ID)
	}
	if record.State == sessionStateUnknown {
		return fmt.Errorf("session %q has an invalid manifest: %s", record.ID, record.LastError)
	}
	args := append([]string(nil), record.Args...)
	cmd := exec.Command(record.Executable, args...)
	cmd.Dir = record.Workspace
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		record.LastError = err.Error()
		_ = saveSessionRecord(record)
		return fmt.Errorf("resume session %q: %w", record.ID, err)
	}
	record.State = sessionStateRunning
	record.Intent = sessionIntentRunning
	record.PID = cmd.Process.Pid
	record.ProcessStart, _, _, _ = processEvidenceFunc(cmd.Process.Pid)
	record.Events = append(record.Events, "resumed by explicit command")
	return saveSessionRecord(record)
}

func stopSession(record TUISessionRecord) error {
	if record.PID > 0 && record.ProcessStart != "" {
		if _, err := sessionProcessIdentity(record); err == nil {
			if proc, err := os.FindProcess(record.PID); err == nil {
				_ = proc.Signal(syscall.SIGTERM)
			}
		}
	}
	record.State = sessionStateStopped
	record.Intent = sessionIntentStopped
	record.Events = append(record.Events, "stopped by explicit command")
	return saveSessionRecord(record)
}

func runSessionCLI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: swarmos session <list|show|resume|stop>")
	}
	switch args[0] {
	case "list":
		records, err := listSessionRecords()
		if err != nil {
			return err
		}
		if len(records) == 0 {
			fmt.Println("no tracked TUI sessions")
			return nil
		}
		for _, record := range records {
			fmt.Printf("%-28s %-12s %-10s %s", record.ID, record.State, record.Intent, record.Workspace)
			if record.Conversation != "" {
				fmt.Printf(" conv=%s", record.Conversation)
			}
			fmt.Println()
		}
		return nil
	case "show":
		if len(args) != 2 {
			return fmt.Errorf("usage: swarmos session show <id>")
		}
		record, err := loadSessionRecord(args[1])
		if err != nil {
			return err
		}
		reconcileSessionRecord(&record)
		data, _ := json.MarshalIndent(record, "", "  ")
		fmt.Println(string(data))
		return saveSessionRecord(record)
	case "resume":
		if len(args) == 2 && args[1] == "--all" {
			records, err := listSessionRecords()
			if err != nil {
				return err
			}
			for _, record := range records {
				if record.State == sessionStateInterrupted && record.Intent == sessionIntentRunning {
					if err := resumeSession(record); err != nil {
						fmt.Fprintf(os.Stderr, "%s: %v\n", record.ID, err)
					}
				}
			}
			return nil
		}
		if len(args) != 2 {
			return fmt.Errorf("usage: swarmos session resume <id>|--all")
		}
		record, err := loadSessionRecord(args[1])
		if err != nil {
			return err
		}
		return resumeSession(record)
	case "stop":
		if len(args) != 2 {
			return fmt.Errorf("usage: swarmos session stop <id>")
		}
		record, err := loadSessionRecord(args[1])
		if err != nil {
			return err
		}
		return stopSession(record)
	default:
		return fmt.Errorf("unknown session subcommand %q — valid: list, show, resume, stop", args[0])
	}
}

func sessionArgs(workspace, conversation, id string) []string {
	args := []string{"--workspace", workspace, "--session-id", id}
	if conversation != "" {
		args = append(args, "--conversation-id", conversation)
	}
	return args
}

func sessionRecordForLaunch(workspace, projectRoot, conversation, handle, id string) TUISessionRecord {
	now := time.Now().UTC()
	executable, _ := os.Executable()
	record := TUISessionRecord{
		Version: 1, ID: id, State: sessionStateRunning, Intent: sessionIntentRunning,
		Workspace: workspace, ProjectRoot: projectRoot, Conversation: conversation,
		Executable: executable, Args: sessionArgs(workspace, conversation, id),
		Handle: handle, PID: os.Getpid(), StartedAt: now, UpdatedAt: now,
		Events: []string{"registered before interactive launch"},
	}
	record.ProcessStart, _, _, _ = processEvidenceFunc(record.PID)
	return record
}

func parseSessionAutoRecovery(args []string) bool {
	for _, arg := range args {
		if arg == "--auto-recover" || arg == "--auto-recover=true" {
			return true
		}
	}
	return false
}

func sessionPIDText(pid int) string {
	if pid <= 0 {
		return "-"
	}
	return strconv.Itoa(pid)
}
