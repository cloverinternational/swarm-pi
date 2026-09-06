// Package client provides a client for the headless TUI automation server.
package client

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/protocol"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/state"
)

// Client connects to the headless TUI automation server.
type Client struct {
	mu       sync.Mutex
	conn     net.Conn
	addr     string
	readBuf  []byte
	handlers map[protocol.MessageType]func(*protocol.Message)
}

// New creates a new client.
func New(addr string) *Client {
	return &Client{
		addr:     addr,
		readBuf:  make([]byte, 0, 65536),
		handlers: make(map[protocol.MessageType]func(*protocol.Message)),
	}
}

// Connect connects to the server.
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	network, address := parseAddress(c.addr)
	conn, err := net.Dial(network, address)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	c.conn = conn
	return nil
}

// Close closes the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// SendKey sends a single key.
func (c *Client) SendKey(key string) error {
	msg, err := protocol.NewCommand(protocol.TypeSendKey, protocol.SendKeyCmd{Key: key})
	if err != nil {
		return err
	}
	return c.sendAndExpectOK(msg)
}

// SendKeys sends multiple keys.
func (c *Client) SendKeys(keys ...string) error {
	msg, err := protocol.NewCommand(protocol.TypeSendKeys, protocol.SendKeysCmd{Keys: keys})
	if err != nil {
		return err
	}
	return c.sendAndExpectOK(msg)
}

// SendText sends text input.
func (c *Client) SendText(text string) error {
	msg, err := protocol.NewCommand(protocol.TypeSendText, protocol.SendTextCmd{Text: text})
	if err != nil {
		return err
	}
	return c.sendAndExpectOK(msg)
}

// Resize resizes the terminal.
func (c *Client) Resize(width, height int) error {
	msg, err := protocol.NewCommand(protocol.TypeResize, protocol.ResizeCmd{Width: width, Height: height})
	if err != nil {
		return err
	}
	return c.sendAndExpectOK(msg)
}

// Click sends a mouse click.
func (c *Client) Click(x, y int) error {
	msg, err := protocol.NewCommand(protocol.TypeClick, protocol.ClickCmd{X: x, Y: y})
	if err != nil {
		return err
	}
	return c.sendAndExpectOK(msg)
}

// Scroll sends a scroll event.
func (c *Client) Scroll(x, y int, up bool) error {
	msg, err := protocol.NewCommand(protocol.TypeScroll, protocol.ScrollCmd{X: x, Y: y, Up: up})
	if err != nil {
		return err
	}
	return c.sendAndExpectOK(msg)
}

// Frame represents a captured frame.
type Frame struct {
	Width      int
	Height     int
	Content    string
	Lines      []string
	FrameNum   int
	RenderTime time.Duration
}

// GetFrame gets the current frame.
func (c *Client) GetFrame() (*Frame, error) {
	msg := &protocol.Message{Type: protocol.TypeGetFrame}
	resp, err := c.sendAndReceive(msg)
	if err != nil {
		return nil, err
	}

	if resp.Type == protocol.TypeError {
		var errResp protocol.ErrorResponse
		resp.ParsePayload(&errResp)
		return nil, fmt.Errorf("server error %d: %s", errResp.Code, errResp.Message)
	}

	var frameResp protocol.FrameResponse
	if err := resp.ParsePayload(&frameResp); err != nil {
		return nil, err
	}

	return &Frame{
		Width:      frameResp.Width,
		Height:     frameResp.Height,
		Content:    frameResp.Content,
		Lines:      frameResp.Lines,
		FrameNum:   frameResp.FrameNum,
		RenderTime: time.Duration(frameResp.RenderTime),
	}, nil
}

// GetState gets the full model state.
func (c *Client) GetState() (*state.FullState, error) {
	msg := &protocol.Message{Type: protocol.TypeGetState}
	resp, err := c.sendAndReceive(msg)
	if err != nil {
		return nil, err
	}

	if resp.Type == protocol.TypeError {
		var errResp protocol.ErrorResponse
		resp.ParsePayload(&errResp)
		return nil, fmt.Errorf("server error %d: %s", errResp.Code, errResp.Message)
	}

	var fullState state.FullState
	if err := resp.ParsePayload(&fullState); err != nil {
		return nil, err
	}

	return &fullState, nil
}

// GetField gets a specific field from the model.
func (c *Client) GetField(path string) (any, bool, error) {
	msg, err := protocol.NewCommand(protocol.TypeGetField, protocol.GetFieldCmd{Path: path})
	if err != nil {
		return nil, false, err
	}

	resp, err := c.sendAndReceive(msg)
	if err != nil {
		return nil, false, err
	}

	if resp.Type == protocol.TypeError {
		var errResp protocol.ErrorResponse
		resp.ParsePayload(&errResp)
		return nil, false, fmt.Errorf("server error %d: %s", errResp.Code, errResp.Message)
	}

	var fieldResp protocol.FieldValueResponse
	if err := resp.ParsePayload(&fieldResp); err != nil {
		return nil, false, err
	}

	return fieldResp.Value, fieldResp.Found, nil
}

// GetA2ADebug retrieves the attached TUI's A2A integration debug snapshot.
// Useful for `swarmos swarm attach <handle> debug` and any other tooling
// that needs to see the peer's live A2A state without guessing.
func (c *Client) GetA2ADebug() (*protocol.A2ADebugResponse, error) {
	msg := &protocol.Message{Type: protocol.TypeGetA2ADebug}
	resp, err := c.sendAndReceive(msg)
	if err != nil {
		return nil, err
	}
	if resp.Type == protocol.TypeError {
		var errResp protocol.ErrorResponse
		resp.ParsePayload(&errResp)
		return nil, fmt.Errorf("server error %d: %s", errResp.Code, errResp.Message)
	}
	var dbg protocol.A2ADebugResponse
	if err := resp.ParsePayload(&dbg); err != nil {
		return nil, err
	}
	return &dbg, nil
}

// Subscribe subscribes to events.
func (c *Client) Subscribe(events ...string) error {
	msg, err := protocol.NewCommand(protocol.TypeSubscribe, protocol.SubscribeCmd{Events: events})
	if err != nil {
		return err
	}
	return c.sendAndExpectOK(msg)
}

// Unsubscribe unsubscribes from events.
func (c *Client) Unsubscribe(events ...string) error {
	msg, err := protocol.NewCommand(protocol.TypeUnsubscribe, protocol.SubscribeCmd{Events: events})
	if err != nil {
		return err
	}
	return c.sendAndExpectOK(msg)
}

// WaitForContent waits for specific content to appear.
func (c *Client) WaitForContent(text string, timeout time.Duration) error {
	msg, err := protocol.NewCommand(protocol.TypeWaitContent, protocol.WaitContentCmd{
		Text:      text,
		TimeoutMs: int(timeout.Milliseconds()),
	})
	if err != nil {
		return err
	}

	// Set longer read timeout for wait operations
	c.conn.SetReadDeadline(time.Now().Add(timeout + time.Second))
	defer c.conn.SetReadDeadline(time.Time{})

	return c.sendAndExpectOK(msg)
}

// OnFrameUpdate sets a handler for frame updates.
func (c *Client) OnFrameUpdate(handler func(*Frame)) {
	c.handlers[protocol.TypeFrameUpdate] = func(msg *protocol.Message) {
		var frameResp protocol.FrameResponse
		if err := msg.ParsePayload(&frameResp); err != nil {
			return
		}
		handler(&Frame{
			Width:      frameResp.Width,
			Height:     frameResp.Height,
			Content:    frameResp.Content,
			Lines:      frameResp.Lines,
			FrameNum:   frameResp.FrameNum,
			RenderTime: time.Duration(frameResp.RenderTime),
		})
	}
}

// OnStateChange sets a handler for state changes.
func (c *Client) OnStateChange(handler func(*state.FullState)) {
	c.handlers[protocol.TypeStateChange] = func(msg *protocol.Message) {
		var fullState state.FullState
		if err := msg.ParsePayload(&fullState); err != nil {
			return
		}
		handler(&fullState)
	}
}

// Listen starts listening for pushed events. Blocks until connection closes.
func (c *Client) Listen() error {
	buf := make([]byte, 65536)
	for {
		n, err := c.conn.Read(buf)
		if err != nil {
			return err
		}

		c.readBuf = append(c.readBuf, buf[:n]...)

		// Process complete messages
		for {
			if len(c.readBuf) < protocol.HeaderSize {
				break
			}

			length, _, err := protocol.DecodeHeader(c.readBuf)
			if err != nil {
				break
			}

			totalLen := int(length) + 4
			if len(c.readBuf) < totalLen {
				break
			}

			msg, err := protocol.Decode(c.readBuf[:totalLen])
			if err != nil {
				c.readBuf = c.readBuf[totalLen:]
				continue
			}

			// Dispatch to handler
			if handler, ok := c.handlers[msg.Type]; ok {
				handler(msg)
			}

			c.readBuf = c.readBuf[totalLen:]
		}
	}
}

// ContainsText checks if the current frame contains text.
func (c *Client) ContainsText(text string) (bool, error) {
	frame, err := c.GetFrame()
	if err != nil {
		return false, err
	}
	return strings.Contains(frame.Content, text), nil
}

// GetText returns the text content of the current frame.
func (c *Client) GetText() (string, error) {
	frame, err := c.GetFrame()
	if err != nil {
		return "", err
	}
	return frame.Content, nil
}

// GetMessages gets messages if the model supports MessageInspector.
func (c *Client) GetMessages() ([]state.MessageState, error) {
	st, err := c.GetState()
	if err != nil {
		return nil, err
	}
	return st.Messages, nil
}

// GetInputText gets input text if the model supports InputInspector.
func (c *Client) GetInputText() (string, error) {
	st, err := c.GetState()
	if err != nil {
		return "", err
	}
	if st.Input == nil {
		return "", nil
	}
	return st.Input.Text, nil
}

func (c *Client) sendAndExpectOK(msg *protocol.Message) error {
	resp, err := c.sendAndReceive(msg)
	if err != nil {
		return err
	}

	if resp.Type == protocol.TypeError {
		var errResp protocol.ErrorResponse
		if err := json.Unmarshal(resp.Payload, &errResp); err != nil {
			return fmt.Errorf("server error (unparseable)")
		}
		return fmt.Errorf("server error %d: %s", errResp.Code, errResp.Message)
	}

	if resp.Type != protocol.TypeOK {
		return fmt.Errorf("unexpected response type: 0x%02x", resp.Type)
	}

	return nil
}

func (c *Client) sendAndReceive(msg *protocol.Message) (*protocol.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Send
	if _, err := c.conn.Write(msg.Encode()); err != nil {
		return nil, fmt.Errorf("write failed: %w", err)
	}

	// Read response
	buf := make([]byte, 65536)
	var respBuf []byte

	for {
		n, err := c.conn.Read(buf)
		if err != nil {
			return nil, fmt.Errorf("read failed: %w", err)
		}

		respBuf = append(respBuf, buf[:n]...)

		if len(respBuf) < protocol.HeaderSize {
			continue
		}

		length, _, err := protocol.DecodeHeader(respBuf)
		if err != nil {
			return nil, err
		}

		totalLen := int(length) + 4
		if len(respBuf) < totalLen {
			continue
		}

		return protocol.Decode(respBuf[:totalLen])
	}
}

func parseAddress(addr string) (network, address string) {
	if after, ok := strings.CutPrefix(addr, "unix://"); ok {
		return "unix", after
	}
	if after, ok := strings.CutPrefix(addr, "tcp://"); ok {
		return "tcp", after
	}
	// Auto-detect Unix socket paths
	if strings.HasPrefix(addr, "/") || strings.HasPrefix(addr, "./") {
		return "unix", addr
	}
	// Default to tcp
	return "tcp", addr
}
