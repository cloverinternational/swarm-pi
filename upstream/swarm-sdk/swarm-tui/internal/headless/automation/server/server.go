// Package server provides a high-performance gnet server for headless TUI automation.
//
// The server runs the TUI headlessly and accepts connections from clients
// that can send commands and receive state updates.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/protocol"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/state"
	"github.com/panjf2000/gnet/v2"
)

// Server is the headless TUI automation server.
type Server struct {
	gnet.BuiltinEventEngine

	mu          sync.RWMutex
	driver      *automation.Driver
	model       tea.Model
	addr        string
	engine      gnet.Engine
	connections map[gnet.Conn]*connState
	subscribers map[gnet.Conn]map[string]bool // conn -> event types

}

type connState struct {
	buffer []byte
}

// Config configures the server.
type Config struct {
	// Address to listen on (e.g., "unix:///tmp/tui.sock" or "tcp://localhost:9999")
	Address string
	// Width is the initial terminal width
	Width int
	// Height is the initial terminal height
	Height int
}

// New creates a new automation server.
func New(model tea.Model, cfg Config) *Server {
	if cfg.Width == 0 {
		cfg.Width = 80
	}
	if cfg.Height == 0 {
		cfg.Height = 24
	}

	driver := automation.NewDriver(model,
		automation.WithSize(cfg.Width, cfg.Height),
	)

	return &Server{
		driver:      driver,
		model:       model,
		addr:        cfg.Address,
		connections: make(map[gnet.Conn]*connState),
		subscribers: make(map[gnet.Conn]map[string]bool),
	}
}

// Start starts the server.
func (s *Server) Start(ctx context.Context) error {
	// Start the driver
	if err := s.driver.Start(); err != nil {
		return fmt.Errorf("failed to start driver: %w", err)
	}

	// Start gnet server
	opts := []gnet.Option{
		gnet.WithMulticore(true),
		gnet.WithReuseAddr(true),
		gnet.WithReusePort(true),
	}

	go func() {
		<-ctx.Done()
		s.Stop()
	}()

	return gnet.Run(s, s.addr, opts...)
}

// Stop stops the server.
func (s *Server) Stop() error {
	if s.engine.CountConnections() > 0 {
		// Close all connections
		s.mu.Lock()
		for conn := range s.connections {
			conn.Close()
		}
		s.mu.Unlock()
	}

	if err := s.engine.Stop(context.Background()); err != nil {
		return err
	}

	return s.driver.Stop()
}

// OnBoot is called when the server starts.
func (s *Server) OnBoot(eng gnet.Engine) gnet.Action {
	s.engine = eng
	return gnet.None
}

// OnOpen is called when a new connection is opened.
func (s *Server) OnOpen(c gnet.Conn) ([]byte, gnet.Action) {
	s.mu.Lock()
	s.connections[c] = &connState{buffer: make([]byte, 0, 4096)}
	s.mu.Unlock()
	return nil, gnet.None
}

// OnClose is called when a connection is closed.
func (s *Server) OnClose(c gnet.Conn, _ error) gnet.Action {
	s.mu.Lock()
	delete(s.connections, c)
	delete(s.subscribers, c)
	s.mu.Unlock()
	return gnet.None
}

// OnTraffic is called when data arrives.
func (s *Server) OnTraffic(c gnet.Conn) gnet.Action {
	s.mu.RLock()
	cs, ok := s.connections[c]
	s.mu.RUnlock()
	if !ok {
		return gnet.Close
	}

	// Read all available data
	data, _ := c.Next(-1)
	cs.buffer = append(cs.buffer, data...)

	// Process complete messages
	for {
		if len(cs.buffer) < protocol.HeaderSize {
			break
		}

		length, _, err := protocol.DecodeHeader(cs.buffer)
		if err != nil {
			s.sendError(c, 400, err.Error())
			return gnet.Close
		}

		totalLen := int(length) + 4 // length field + payload
		if len(cs.buffer) < totalLen {
			break
		}

		msg, err := protocol.Decode(cs.buffer[:totalLen])
		if err != nil {
			s.sendError(c, 400, err.Error())
			return gnet.Close
		}

		// Process message
		s.handleMessage(c, msg)

		// Remove processed message from buffer
		cs.buffer = cs.buffer[totalLen:]
	}

	return gnet.None
}

func (s *Server) handleMessage(c gnet.Conn, msg *protocol.Message) {
	switch msg.Type {
	case protocol.TypeSendKey:
		var cmd protocol.SendKeyCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			s.sendError(c, 400, err.Error())
			return
		}
		if err := s.driver.SendKey(cmd.Key); err != nil {
			s.sendError(c, 500, err.Error())
			return
		}
		s.sendOK(c)
		s.notifySubscribers("frame")

	case protocol.TypeSendKeys:
		var cmd protocol.SendKeysCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			s.sendError(c, 400, err.Error())
			return
		}
		if err := s.driver.SendKeys(cmd.Keys...); err != nil {
			s.sendError(c, 500, err.Error())
			return
		}
		s.sendOK(c)
		s.notifySubscribers("frame")

	case protocol.TypeSendText:
		var cmd protocol.SendTextCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			s.sendError(c, 400, err.Error())
			return
		}
		if err := s.driver.SendText(cmd.Text); err != nil {
			s.sendError(c, 500, err.Error())
			return
		}
		s.sendOK(c)
		s.notifySubscribers("frame")

	case protocol.TypeResize:
		var cmd protocol.ResizeCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			s.sendError(c, 400, err.Error())
			return
		}
		if err := s.driver.Resize(cmd.Width, cmd.Height); err != nil {
			s.sendError(c, 500, err.Error())
			return
		}
		s.sendOK(c)
		s.notifySubscribers("frame")

	case protocol.TypeClick:
		var cmd protocol.ClickCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			s.sendError(c, 400, err.Error())
			return
		}
		if err := s.driver.Click(cmd.X, cmd.Y); err != nil {
			s.sendError(c, 500, err.Error())
			return
		}
		s.sendOK(c)
		s.notifySubscribers("frame")

	case protocol.TypeScroll:
		var cmd protocol.ScrollCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			s.sendError(c, 400, err.Error())
			return
		}
		if err := s.driver.Scroll(cmd.X, cmd.Y, cmd.Up); err != nil {
			s.sendError(c, 500, err.Error())
			return
		}
		s.sendOK(c)
		s.notifySubscribers("frame")

	case protocol.TypeGetFrame:
		frame := s.driver.GetFrame()
		resp := protocol.NewFrame(
			frame.Width,
			frame.Height,
			frame.Content,
			frame.Lines,
			s.driver.GetFrameCount(),
			frame.RenderTime.Nanoseconds(),
		)
		c.Write(resp.Encode())

	case protocol.TypeGetState:
		model := s.driver.Model()
		fullState := state.Extract(model)
		resp := protocol.NewState(fullState)
		c.Write(resp.Encode())

	case protocol.TypeGetField:
		var cmd protocol.GetFieldCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			s.sendError(c, 400, err.Error())
			return
		}
		model := s.driver.Model()
		value, found := state.ExtractField(model, cmd.Path)
		data, _ := json.Marshal(protocol.FieldValueResponse{
			Path:  cmd.Path,
			Value: value,
			Found: found,
		})
		resp := &protocol.Message{Type: protocol.TypeFieldValue, Payload: data}
		c.Write(resp.Encode())

	case protocol.TypeSubscribe:
		var cmd protocol.SubscribeCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			s.sendError(c, 400, err.Error())
			return
		}
		s.mu.Lock()
		if s.subscribers[c] == nil {
			s.subscribers[c] = make(map[string]bool)
		}
		for _, event := range cmd.Events {
			s.subscribers[c][event] = true
		}
		s.mu.Unlock()
		s.sendOK(c)

	case protocol.TypeUnsubscribe:
		var cmd protocol.SubscribeCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			s.sendError(c, 400, err.Error())
			return
		}
		s.mu.Lock()
		if s.subscribers[c] != nil {
			for _, event := range cmd.Events {
				delete(s.subscribers[c], event)
			}
		}
		s.mu.Unlock()
		s.sendOK(c)

	case protocol.TypeWaitContent:
		var cmd protocol.WaitContentCmd
		if err := msg.ParsePayload(&cmd); err != nil {
			s.sendError(c, 400, err.Error())
			return
		}
		timeout := time.Duration(cmd.TimeoutMs) * time.Millisecond
		if err := s.driver.WaitForContent(cmd.Text, timeout); err != nil {
			s.sendError(c, 408, err.Error())
			return
		}
		s.sendOK(c)

	default:
		s.sendError(c, 400, fmt.Sprintf("unknown message type: 0x%02x", msg.Type))
	}
}

func (s *Server) sendOK(c gnet.Conn) {
	c.Write(protocol.NewOK().Encode())
}

func (s *Server) sendError(c gnet.Conn, code int, message string) {
	c.Write(protocol.NewError(code, message).Encode())
}

func (s *Server) notifySubscribers(eventType string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for conn, events := range s.subscribers {
		if !events[eventType] {
			continue
		}

		switch eventType {
		case "frame":
			frame := s.driver.GetFrame()
			resp := protocol.NewFrame(
				frame.Width,
				frame.Height,
				frame.Content,
				frame.Lines,
				s.driver.GetFrameCount(),
				frame.RenderTime.Nanoseconds(),
			)
			resp.Type = protocol.TypeFrameUpdate
			conn.AsyncWrite(resp.Encode(), nil)

		case "state":
			model := s.driver.Model()
			fullState := state.Extract(model)
			resp := protocol.NewState(fullState)
			resp.Type = protocol.TypeStateChange
			conn.AsyncWrite(resp.Encode(), nil)
		}
	}
}

// GetDriver returns the underlying driver for direct access in tests.
func (s *Server) GetDriver() *automation.Driver {
	return s.driver
}
