// Package main is a Go port of noemica-io/open-claude-in-chrome's mcp-server.js.
// It exposes all 18 browser-automation tools via a minimal MCP stdio server and
// bridges tool calls to the browser extension over a local TCP connection.
//
// Architecture (unchanged from the original Node.js project):
//
//	Claude Code / Swarm <--stdio MCP--> chrome-mcp (this) <--TCP:18765--> chrome-native-host <--native msg--> Extension <--> Browser
//
// IMPORTANT: We implement the MCP stdio protocol manually (bufio.Scanner +
// json.Encoder) rather than using the official go-sdk mcp.StdioTransport,
// because the go-sdk crashes on the swarm MCP client's
// "notifications/initialized" message (protocol version mismatch).
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/vision"
)

// ─── Config ──────────────────────────────────────────────────────────────────

const defaultPort = 18765

type fileConfig struct {
	Port int `json:"port"`
}

func getPort() int {
	cfgPath := filepath.Join(os.Getenv("HOME"), ".config", "open-claude-in-chrome", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return defaultPort
	}
	var fc fileConfig
	if json.Unmarshal(data, &fc) == nil && fc.Port > 0 {
		return fc.Port
	}
	return defaultPort
}

// ─── MCP JSON-RPC types (matching swarm's internal/tools/mcp protocol) ───────

type mcpRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *mcpError      `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ─── TCP wire types ───────────────────────────────────────────────────────────

type wireMsg struct {
	ID       string          `json:"id,omitempty"`
	Type     string          `json:"type"`
	Tool     string          `json:"tool,omitempty"`
	Args     map[string]any  `json:"args,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
	ClientID string          `json:"clientId,omitempty"`
}

// ─── Pending request tracking ─────────────────────────────────────────────────

type pendingReq struct {
	resultCh chan json.RawMessage
	errCh    chan error
	timer    *time.Timer
	tool     string
	args     map[string]any
	resent   bool
}

type clientInfo struct {
	clientID int
	origID   string
}

// ─── Bridge ───────────────────────────────────────────────────────────────────

type bridge struct {
	mu sync.Mutex

	mode string
	port int

	tcpLn        net.Listener
	nativeConn   net.Conn
	clientConns  map[int]net.Conn
	clientIDCtr  int
	clientReqMap map[string]clientInfo

	primaryConn net.Conn

	pending map[string]*pendingReq
	reqCtr  int64
	pidPath string
}

func newBridge(port int) *bridge {
	return &bridge{
		mode:         "primary",
		port:         port,
		clientConns:  make(map[int]net.Conn),
		clientReqMap: make(map[string]clientInfo),
		pending:      make(map[string]*pendingReq),
		pidPath:      filepath.Join(os.TempDir(), fmt.Sprintf("open-claude-in-chrome-mcp-%d.pid", port)),
	}
}

func (b *bridge) sendToExtension(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	id := strconv.FormatInt(atomic.AddInt64(&b.reqCtr, 1), 10)

	pr := &pendingReq{
		resultCh: make(chan json.RawMessage, 1),
		errCh:    make(chan error, 1),
		tool:     tool,
		args:     args,
	}

	b.mu.Lock()
	b.pending[id] = pr
	b.mu.Unlock()

	pr.timer = time.AfterFunc(60*time.Second, func() {
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		pr.errCh <- fmt.Errorf("tool request timed out after 60s")
	})

	msg := wireMsg{ID: id, Type: "tool_request", Tool: tool, Args: args}

	b.mu.Lock()
	var writeErr error
	if b.mode == "primary" {
		if b.nativeConn == nil {
			writeErr = fmt.Errorf("browser extension is not connected — make sure a supported Chromium browser is running with the Open Claude in Chrome extension installed and enabled")
		} else {
			writeErr = writeConn(b.nativeConn, msg)
		}
	} else {
		if b.primaryConn == nil {
			writeErr = fmt.Errorf("lost connection to primary MCP server")
		} else {
			writeErr = writeConn(b.primaryConn, msg)
		}
	}
	b.mu.Unlock()

	if writeErr != nil {
		pr.timer.Stop()
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return nil, writeErr
	}

	select {
	case <-ctx.Done():
		pr.timer.Stop()
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return nil, ctx.Err()
	case result := <-pr.resultCh:
		return result, nil
	case err := <-pr.errCh:
		return nil, err
	}
}

func writeConn(conn net.Conn, msg wireMsg) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = conn.Write(append(data, '\n'))
	return err
}

func (b *bridge) handleResponse(msg wireMsg) {
	if msg.ID != "" {
		b.mu.Lock()
		ci, isClient := b.clientReqMap[msg.ID]
		if isClient {
			delete(b.clientReqMap, msg.ID)
			clientConn := b.clientConns[ci.clientID]
			b.mu.Unlock()
			if clientConn != nil {
				fwd := wireMsg{ID: ci.origID, Type: msg.Type, Result: msg.Result, Error: msg.Error}
				_ = writeConn(clientConn, fwd)
			}
			return
		}
		b.mu.Unlock()
	}

	if msg.ID == "" {
		return
	}
	b.mu.Lock()
	pr, ok := b.pending[msg.ID]
	if !ok {
		b.mu.Unlock()
		return
	}
	delete(b.pending, msg.ID)
	b.mu.Unlock()

	pr.timer.Stop()
	if msg.Type == "tool_error" {
		errMsg := msg.Error
		if errMsg == "" {
			errMsg = "tool execution failed"
		}
		pr.errCh <- fmt.Errorf("%s", errMsg)
	} else {
		pr.resultCh <- msg.Result
	}
}

func (b *bridge) startPrimary() error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", b.port))
	if err != nil {
		return err
	}
	b.tcpLn = ln
	b.mode = "primary"
	_ = os.WriteFile(b.pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644)
	fmt.Fprintf(os.Stderr, "Primary MCP server listening on :%d\n", b.port)
	go b.acceptLoop()
	return nil
}

func (b *bridge) acceptLoop() {
	for {
		conn, err := b.tcpLn.Accept()
		if err != nil {
			return
		}
		go b.classifyConn(conn)
	}
}

func (b *bridge) classifyConn(conn net.Conn) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)

	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	classified := false
	for !classified {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for i, byt := range buf {
				if byt == '\n' {
					firstLine := string(buf[:i])
					buf = buf[i+1:]
					_ = conn.SetReadDeadline(time.Time{})
					var msg wireMsg
					if json.Unmarshal([]byte(firstLine), &msg) == nil && msg.Type == "client_hello" {
						go b.setupClientConn(conn, buf)
					} else {
						go b.setupNativeHost(conn, append([]byte(firstLine+"\n"), buf...))
					}
					classified = true
					break
				}
			}
		}
		if err != nil {
			_ = conn.SetReadDeadline(time.Time{})
			go b.setupNativeHost(conn, buf)
			classified = true
		}
	}
}

func (b *bridge) setupNativeHost(conn net.Conn, initial []byte) {
	b.mu.Lock()
	if b.nativeConn != nil {
		b.mu.Unlock()
		_ = writeConn(conn, wireMsg{Type: "error", Error: "another browser profile is already connected"})
		conn.Close()
		return
	}
	b.nativeConn = conn
	b.mu.Unlock()

	fmt.Fprintln(os.Stderr, "Browser extension (native host) connected")

	b.readLines(conn, initial, func(msg wireMsg) {
		if msg.Type == "heartbeat" {
			return
		}
		b.handleResponse(msg)
	})

	b.mu.Lock()
	if b.nativeConn == conn {
		b.nativeConn = nil
	}
	pending := make(map[string]*pendingReq, len(b.pending))
	for k, v := range b.pending {
		pending[k] = v
	}
	b.mu.Unlock()

	fmt.Fprintln(os.Stderr, "Native host disconnected; waiting 5s for reconnect…")
	time.Sleep(5 * time.Second)

	b.mu.Lock()
	newNative := b.nativeConn
	b.mu.Unlock()

	if newNative != nil {
		for id, pr := range pending {
			if pr.resent {
				continue
			}
			pr.resent = true
			_ = writeConn(newNative, wireMsg{ID: id, Type: "tool_request", Tool: pr.tool, Args: pr.args})
		}
	} else {
		for id, pr := range pending {
			pr.timer.Stop()
			b.mu.Lock()
			delete(b.pending, id)
			b.mu.Unlock()
			pr.errCh <- fmt.Errorf("native host disconnected")
		}
	}
}

func (b *bridge) setupClientConn(conn net.Conn, initial []byte) {
	b.mu.Lock()
	b.clientIDCtr++
	clientID := b.clientIDCtr
	b.clientConns[clientID] = conn
	b.mu.Unlock()

	fmt.Fprintf(os.Stderr, "Client MCP server connected (client %d)\n", clientID)
	_ = writeConn(conn, wireMsg{Type: "client_ack", ClientID: strconv.Itoa(clientID)})

	b.readLines(conn, initial, func(msg wireMsg) {
		if msg.Type != "tool_request" || msg.ID == "" {
			return
		}
		prefixedID := fmt.Sprintf("c%d_%s", clientID, msg.ID)
		b.mu.Lock()
		b.clientReqMap[prefixedID] = clientInfo{clientID: clientID, origID: msg.ID}
		native := b.nativeConn
		b.mu.Unlock()

		if native == nil {
			_ = writeConn(conn, wireMsg{ID: msg.ID, Type: "tool_error", Error: "browser extension is not connected"})
			b.mu.Lock()
			delete(b.clientReqMap, prefixedID)
			b.mu.Unlock()
			return
		}
		_ = writeConn(native, wireMsg{ID: prefixedID, Type: "tool_request", Tool: msg.Tool, Args: msg.Args})
	})

	b.mu.Lock()
	delete(b.clientConns, clientID)
	for k, ci := range b.clientReqMap {
		if ci.clientID == clientID {
			delete(b.clientReqMap, k)
		}
	}
	b.mu.Unlock()
	fmt.Fprintf(os.Stderr, "Client MCP server disconnected (client %d)\n", clientID)
}

func (b *bridge) startClient() {
	b.mode = "client"
	fmt.Fprintf(os.Stderr, "Port %d in use — connecting as client…\n", b.port)
	go b.clientConnectLoop()
}

func (b *bridge) clientConnectLoop() {
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", b.port), 5*time.Second)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		fmt.Fprintf(os.Stderr, "Connected to primary on :%d\n", b.port)
		b.mu.Lock()
		b.primaryConn = conn
		b.mu.Unlock()

		_ = writeConn(conn, wireMsg{Type: "client_hello"})

		b.readLines(conn, nil, func(msg wireMsg) {
			switch msg.Type {
			case "client_ack":
			case "error":
				fmt.Fprintf(os.Stderr, "Primary error: %s\n", msg.Error)
			default:
				if msg.ID == "" {
					return
				}
				b.mu.Lock()
				pr, ok := b.pending[msg.ID]
				if ok {
					delete(b.pending, msg.ID)
				}
				b.mu.Unlock()
				if !ok {
					return
				}
				pr.timer.Stop()
				if msg.Type == "tool_error" {
					errMsg := msg.Error
					if errMsg == "" {
						errMsg = "tool execution failed"
					}
					pr.errCh <- fmt.Errorf("%s", errMsg)
				} else {
					pr.resultCh <- msg.Result
				}
			}
		})

		b.mu.Lock()
		b.primaryConn = nil
		for id, pr := range b.pending {
			pr.timer.Stop()
			delete(b.pending, id)
			pr.errCh <- fmt.Errorf("primary MCP server disconnected")
		}
		b.mu.Unlock()
		time.Sleep(2 * time.Second)
	}
}

func (b *bridge) readLines(conn net.Conn, initial []byte, fn func(wireMsg)) {
	// Process initial buffer.
	if len(initial) > 0 {
		start := 0
		for i, ch := range initial {
			if ch == '\n' {
				line := string(initial[start:i])
				start = i + 1
				if line == "" {
					continue
				}
				var msg wireMsg
				if json.Unmarshal([]byte(line), &msg) == nil {
					fn(msg)
				}
			}
		}
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var msg wireMsg
		if json.Unmarshal([]byte(line), &msg) == nil {
			fn(msg)
		}
	}
}

func (b *bridge) shutdown() {
	if b.mode == "primary" {
		if data, err := os.ReadFile(b.pidPath); err == nil {
			if string(data) == strconv.Itoa(os.Getpid()) {
				_ = os.Remove(b.pidPath)
			}
		}
		if b.tcpLn != nil {
			b.tcpLn.Close()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.nativeConn != nil {
		b.nativeConn.Close()
	}
	if b.primaryConn != nil {
		b.primaryConn.Close()
	}
	for _, c := range b.clientConns {
		c.Close()
	}
	for id, pr := range b.pending {
		pr.timer.Stop()
		delete(b.pending, id)
		pr.errCh <- fmt.Errorf("server shutting down")
	}
}

// ─── MCP result helpers ───────────────────────────────────────────────────────

func makeTextResult(text string) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
	}
}

func makeErrResult(text string) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": true,
	}
}

func convertExtensionResult(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return makeTextResult("")
	}
	// Plain string result.
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return makeTextResult(str)
	}
	// Already a content array object.
	var obj struct {
		Content []json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &obj) == nil && len(obj.Content) > 0 {
		var items []any
		for _, item := range obj.Content {
			var part struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Data     string `json:"data"`
				MIMEType string `json:"mimeType"`
			}
			if json.Unmarshal(item, &part) != nil {
				continue
			}
			switch part.Type {
			case "text":
				items = append(items, map[string]any{"type": "text", "text": part.Text})
			case "image":
				// Validate base64 is valid.
				if _, err := base64.StdEncoding.DecodeString(part.Data); err != nil {
					_, err = base64.RawStdEncoding.DecodeString(part.Data)
				}
				mime := part.MIMEType
				if mime == "" {
					mime = "image/png"
				}
				// The extension frequently encodes large screenshots (e.g.
				// zoom regions) as JPEG for size but still leaves mimeType
				// unset or stale, which previously defaulted straight to
				// "image/png" here regardless of the real bytes. Anthropic
				// sniffs the actual content server-side and hard-rejects the
				// whole request ("all providers exhausted") when the
				// declared media_type disagrees, so always reconcile against
				// the real bytes before trusting the extension's claim (or
				// our own png fallback).
				mime = vision.ReconcileMediaType(part.Data, mime)
				items = append(items, map[string]any{"type": "image", "data": part.Data, "mimeType": mime})
			}
		}
		if len(items) > 0 {
			return map[string]any{"content": items}
		}
	}
	// Fallback: JSON-encode as text.
	var tmp any
	pretty := raw
	if json.Unmarshal(raw, &tmp) == nil {
		if b, err := json.MarshalIndent(tmp, "", "  "); err == nil {
			pretty = b
		}
	}
	return makeTextResult(string(pretty))
}

// ─── Tool definitions (JSON Schema for all 18 tools) ─────────────────────────

func toolsList() []map[string]any {
	str := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	num := func(desc string) map[string]any {
		return map[string]any{"type": "number", "description": desc}
	}
	boolProp := func(desc string) map[string]any {
		return map[string]any{"type": "boolean", "description": desc}
	}
	arr := func(items map[string]any, desc string) map[string]any {
		return map[string]any{"type": "array", "items": items, "description": desc}
	}
	numArr := func(desc string) map[string]any {
		return arr(map[string]any{"type": "number"}, desc)
	}
	obj := func(props map[string]any, req []string, desc string) map[string]any {
		s := map[string]any{"type": "object", "description": desc, "properties": props}
		if len(req) > 0 {
			s["required"] = req
		}
		return s
	}
	tool := func(name, desc string, schema map[string]any) map[string]any {
		return map[string]any{"name": name, "description": desc, "inputSchema": schema}
	}

	tabID := num("Tab ID (use tabs_context_mcp first if unknown)")

	return []map[string]any{
		tool("tabs_context_mcp",
			"Get context about current MCP tab group. CRITICAL: call before using other browser tools. Each new conversation should create its own tab via tabs_create_mcp.",
			obj(map[string]any{"createIfEmpty": boolProp("Create a new tab group if none exists")}, nil, "")),

		tool("tabs_create_mcp",
			"Create a new empty tab in the MCP tab group.",
			obj(map[string]any{}, nil, "")),

		tool("navigate",
			"Navigate to a URL or go forward/back in history.",
			obj(map[string]any{
				"url":   str("URL to navigate to, or 'forward'/'back'"),
				"tabId": tabID,
			}, []string{"url", "tabId"}, "")),

		tool("computer",
			"Mouse and keyboard interaction plus screenshots. Actions: left_click right_click double_click triple_click type screenshot wait scroll key left_click_drag zoom scroll_to hover",
			obj(map[string]any{
				"action":           str("Action to perform"),
				"tabId":            tabID,
				"coordinate":       numArr("[x, y] pixel coordinates"),
				"duration":         num("Seconds to wait (max 30)"),
				"modifiers":        str("Modifier keys: ctrl shift alt cmd"),
				"ref":              str("Element reference ID from read_page/find"),
				"region":           numArr("[x0, y0, x1, y1] region for zoom"),
				"repeat":           num("Times to repeat key action (1-100)"),
				"scroll_direction": str("up down left right"),
				"scroll_amount":    num("Scroll ticks (default 3)"),
				"start_coordinate": numArr("[x, y] start for left_click_drag"),
				"text":             str("Text to type or key(s) to press"),
			}, []string{"action", "tabId"}, "")),

		tool("find",
			"Find elements by natural language (e.g. 'search bar', 'login button'). Returns up to 20 matches.",
			obj(map[string]any{
				"query": str("Natural language description of what to find"),
				"tabId": tabID,
			}, []string{"query", "tabId"}, "")),

		tool("form_input",
			"Set value in a form element by ref ID from read_page.",
			obj(map[string]any{
				"ref":   str("Element reference ID e.g. ref_1"),
				"value": map[string]any{"description": "Value: string boolean or number"},
				"tabId": tabID,
			}, []string{"ref", "value", "tabId"}, "")),

		tool("get_page_text",
			"Extract plain text from the page, prioritising article content.",
			obj(map[string]any{"tabId": tabID}, []string{"tabId"}, "")),

		tool("gif_creator",
			"Manage GIF recording. Actions: start_recording stop_recording export clear. Take screenshot immediately after start and before stop.",
			obj(map[string]any{
				"action":   str("start_recording stop_recording export clear"),
				"tabId":    tabID,
				"download": boolProp("True to download GIF (export only)"),
				"filename": str("Optional filename for exported GIF"),
				"options": obj(map[string]any{
					"showClickIndicators": boolProp("Show click indicators"),
					"showDragPaths":       boolProp("Show drag paths"),
					"showActionLabels":    boolProp("Show action labels"),
					"showProgressBar":     boolProp("Show progress bar"),
					"showWatermark":       boolProp("Show watermark"),
					"quality":             num("Quality 1-30 lower=better"),
				}, nil, "GIF options for export action"),
			}, []string{"action", "tabId"}, "")),

		tool("javascript_tool",
			"Execute JavaScript in the page context. Use last expression as result — do NOT use return statements.",
			obj(map[string]any{
				"action": str("Must be 'javascript_exec'"),
				"text":   str("JavaScript code to execute"),
				"tabId":  tabID,
			}, []string{"action", "text", "tabId"}, "")),

		tool("read_console_messages",
			"Read browser console messages. Always provide a pattern to filter results.",
			obj(map[string]any{
				"tabId":      tabID,
				"pattern":    str("Regex pattern to filter messages"),
				"limit":      num("Max messages (default 100)"),
				"onlyErrors": boolProp("Only return errors"),
				"clear":      boolProp("Clear messages after reading"),
			}, []string{"tabId"}, "")),

		tool("read_network_requests",
			"Read HTTP network requests from the tab.",
			obj(map[string]any{
				"tabId":      tabID,
				"urlPattern": str("URL substring filter"),
				"limit":      num("Max requests (default 100)"),
				"clear":      boolProp("Clear after reading"),
			}, []string{"tabId"}, "")),

		tool("read_page",
			"Get accessibility tree of the page (default all elements, capped at 50000 chars).",
			obj(map[string]any{
				"tabId":     tabID,
				"filter":    str("'interactive' or 'all' (default)"),
				"depth":     num("Max tree depth (default 15)"),
				"ref_id":    str("Parent element ref to focus on"),
				"max_chars": num("Max output chars (default 50000)"),
			}, []string{"tabId"}, "")),

		tool("resize_window",
			"Resize the browser window.",
			obj(map[string]any{
				"width":  num("Target width in pixels"),
				"height": num("Target height in pixels"),
				"tabId":  tabID,
			}, []string{"width", "height", "tabId"}, "")),

		tool("shortcuts_list",
			"List available shortcuts and workflows.",
			obj(map[string]any{"tabId": tabID}, []string{"tabId"}, "")),

		tool("shortcuts_execute",
			"Execute a shortcut or workflow. Use shortcuts_list first.",
			obj(map[string]any{
				"tabId":      tabID,
				"shortcutId": str("Shortcut ID"),
				"command":    str("Command name without leading slash"),
			}, []string{"tabId"}, "")),

		tool("switch_browser",
			"Switch which Chromium browser is used. Broadcasts a connection request.",
			obj(map[string]any{}, nil, "")),

		tool("update_plan",
			"Present a plan for approval before taking browser actions.",
			obj(map[string]any{
				"domains":  arr(map[string]any{"type": "string"}, "Domains you will visit"),
				"approach": arr(map[string]any{"type": "string"}, "3-7 item description of what you will do"),
			}, []string{"domains", "approach"}, "")),

		tool("upload_image",
			"Upload a screenshot or image to a file input or drag-drop target. Provide ref OR coordinate, not both.",
			obj(map[string]any{
				"imageId":    str("ID of captured screenshot or uploaded image"),
				"tabId":      tabID,
				"ref":        str("Element reference for file inputs"),
				"coordinate": numArr("[x, y] coordinates for drag-drop"),
				"filename":   str("Optional filename (default: image.png)"),
			}, []string{"imageId", "tabId"}, "")),
	}
}

// ─── MCP stdio server loop ────────────────────────────────────────────────────

func runMCPServer(ctx context.Context, b *bridge) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	enc := json.NewEncoder(os.Stdout)

	respond := func(id any, result map[string]any, mcpErr *mcpError) {
		_ = enc.Encode(mcpResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result:  result,
			Error:   mcpErr,
		})
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !scanner.Scan() {
			return // stdin closed — session ended
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req mcpRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue // ignore malformed JSON
		}
		if req.ID == nil {
			continue // notification — no response needed
		}

		switch req.Method {
		case "initialize":
			respond(req.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities": map[string]any{
					"tools": map[string]any{"listChanged": false},
				},
				"serverInfo": map[string]any{
					"name":    "open-claude-in-chrome",
					"version": "1.0.0",
				},
			}, nil)

		case "tools/list":
			respond(req.ID, map[string]any{"tools": toolsList()}, nil)

		case "tools/call":
			toolName, _ := req.Params["name"].(string)
			args, _ := req.Params["arguments"].(map[string]any)
			if args == nil {
				args = map[string]any{}
			}

			raw, err := b.sendToExtension(ctx, toolName, args)
			if err != nil {
				respond(req.ID, makeErrResult(fmt.Sprintf("Error: %v", err)), nil)
			} else {
				respond(req.ID, convertExtensionResult(raw), nil)
			}

		case "ping":
			respond(req.ID, map[string]any{}, nil)

		default:
			respond(req.ID, nil, &mcpError{Code: -32601, Message: "method not found: " + req.Method})
		}
	}
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	port := getPort()
	b := newBridge(port)

	// Clean stale pidfile.
	if data, err := os.ReadFile(b.pidPath); err == nil {
		if oldPid, err := strconv.Atoi(string(data)); err == nil && oldPid != os.Getpid() {
			proc, err2 := os.FindProcess(oldPid)
			if err2 != nil || proc.Signal(syscall.Signal(0)) != nil {
				_ = os.Remove(b.pidPath)
			}
		}
	}

	if err := b.startPrimary(); err != nil {
		if isAddrInUse(err) {
			b.startClient()
		} else {
			fmt.Fprintf(os.Stderr, "TCP server error: %v\n", err)
			os.Exit(1)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-sigCh
		b.shutdown()
		cancel()
	}()

	runMCPServer(ctx, b)
	b.shutdown()
}

func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return containsStr(s, "address already in use") || containsStr(s, "EADDRINUSE")
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
