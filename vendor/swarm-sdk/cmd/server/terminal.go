// Package main provides PTY terminal WebSocket support for Swarm Canvas
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// TerminalSession represents an active PTY session
type TerminalSession struct {
	ID     string          `json:"id"`
	Title  string          `json:"title"`
	Cwd    string          `json:"cwd"`
	Shell  string          `json:"shell"`
	PTY    *os.File        `json:"-"`
	Cmd    *exec.Cmd       `json:"-"`
	WS     *websocket.Conn `json:"-"`
	Mu     sync.Mutex      `json:"-"`
	Active bool            `json:"-"`
	Buffer []string        `json:"-"` // Output buffer for replay
	Cols   int             `json:"cols"`
	Rows   int             `json:"rows"`
}

// TerminalMessage represents WebSocket messages
type TerminalMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	ID   string `json:"id,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// TerminalManager manages all terminal sessions
type TerminalManager struct {
	sessions   map[string]*TerminalSession
	mu         sync.RWMutex
	upgrader   websocket.Upgrader
	sessionNum int
	logger     *log.Logger
}

// NewTerminalManager creates a new terminal manager
func NewTerminalManager() *TerminalManager {
	return &TerminalManager{
		sessions: make(map[string]*TerminalSession),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for development
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
		logger: log.New(os.Stdout, "[terminal] ", log.LstdFlags),
	}
}

// HandleWebSocket upgrades HTTP to WebSocket and manages the terminal session
func (tm *TerminalManager) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Get query params
	action := r.URL.Query().Get("action")
	cwd := r.URL.Query().Get("cwd")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	// Upgrade connection
	ws, err := tm.upgrader.Upgrade(w, r, nil)
	if err != nil {
		tm.logger.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	// Note: We do NOT defer ws.Close() here - the session manages its own lifecycle
	// The WebSocket is closed in closeSession() when the PTY exits or client disconnects

	if action == "create" || action == "" {
		tm.createSession(ws, cwd)
	}
}

// createSession creates a new PTY session
func (tm *TerminalManager) createSession(ws *websocket.Conn, cwd string) {
	tm.sessionNum++
	sessionID := fmt.Sprintf("term-%d", tm.sessionNum)
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	// Start shell with PTY
	cmd := exec.Command(shell, "-l")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		tm.logger.Printf("Failed to start PTY: %v", err)
		ws.WriteJSON(TerminalMessage{Type: "error", Data: err.Error()})
		return
	}

	session := &TerminalSession{
		ID:     sessionID,
		Title:  fmt.Sprintf("Terminal %d", tm.sessionNum),
		Cwd:    cwd,
		Shell:  shell,
		PTY:    ptmx,
		Cmd:    cmd,
		WS:     ws,
		Active: true,
		Buffer: make([]string, 0, 500),
		Cols:   80,
		Rows:   24,
	}

	tm.mu.Lock()
	tm.sessions[sessionID] = session
	tm.mu.Unlock()

	// Send connected message
	ws.WriteJSON(TerminalMessage{
		Type: "connected",
		ID:   sessionID,
		Data: session.Title,
	})

	// Start goroutines for I/O
	go tm.readFromPTY(session)
	go tm.readFromWS(session)

	// Wait for command to exit
	go func() {
		cmd.Wait()
		tm.closeSession(sessionID)
	}()

	tm.logger.Printf("Created terminal session %s in %s", sessionID, cwd)
}

// readFromPTY reads output from PTY and sends to WebSocket
func (tm *TerminalManager) readFromPTY(session *TerminalSession) {
	buf := make([]byte, 1024)
	for {
		n, err := session.PTY.Read(buf)
		if err != nil {
			if session.Active {
				tm.logger.Printf("PTY read error for %s: %v", session.ID, err)
			}
			return
		}

		data := string(buf[:n])

		// Add to buffer
		session.Mu.Lock()
		if len(session.Buffer) < 500 {
			session.Buffer = append(session.Buffer, data)
		}
		session.Mu.Unlock()

		// Send to WebSocket
		session.Mu.Lock()
		if session.WS != nil {
			err := session.WS.WriteJSON(TerminalMessage{
				Type: "output",
				Data: data,
			})
			if err != nil {
				session.Mu.Unlock()
				return
			}
		}
		session.Mu.Unlock()
	}
}

// readFromWS reads input from WebSocket and writes to PTY
func (tm *TerminalManager) readFromWS(session *TerminalSession) {
	for {
		var msg TerminalMessage
		err := session.WS.ReadJSON(&msg)
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				tm.logger.Printf("WebSocket closed for %s", session.ID)
			} else {
				tm.logger.Printf("WebSocket read error for %s: %v", session.ID, err)
			}
			tm.closeSession(session.ID)
			return
		}

		switch msg.Type {
		case "input":
			_, err := session.PTY.Write([]byte(msg.Data))
			if err != nil {
				tm.logger.Printf("PTY write error for %s: %v", session.ID, err)
				return
			}

		case "resize":
			session.Cols = msg.Cols
			session.Rows = msg.Rows
			ws := &pty.Winsize{
				Cols: uint16(msg.Cols),
				Rows: uint16(msg.Rows),
			}
			pty.Setsize(session.PTY, ws)
		}
	}
}

// closeSession closes a terminal session
func (tm *TerminalManager) closeSession(id string) {
	tm.mu.Lock()
	session, exists := tm.sessions[id]
	if !exists {
		tm.mu.Unlock()
		return
	}

	session.Active = false
	delete(tm.sessions, id)
	tm.mu.Unlock()

	// Notify client
	session.Mu.Lock()
	if session.WS != nil {
		session.WS.WriteJSON(TerminalMessage{Type: "exit", ID: id})
		session.WS.Close()
		session.WS = nil
	}
	session.Mu.Unlock()

	// Cleanup PTY
	if session.PTY != nil {
		session.PTY.Close()
	}
	if session.Cmd != nil && session.Cmd.Process != nil {
		syscall.Kill(-session.Cmd.Process.Pid, syscall.SIGTERM)
	}

	tm.logger.Printf("Closed terminal session %s", id)
}

// SetupTerminalRoutes sets up WebSocket route for terminal
func (tm *TerminalManager) SetupTerminalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws/terminal", tm.HandleWebSocket)
}
