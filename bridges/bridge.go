// Package bridges provides authenticated RPC and event transports for Pi workers.
package bridges

import (
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Identity struct{ User, Session string }
type VerifyToken func(string) (string, bool)
type Invoker func(context.Context, Identity, string, json.RawMessage) (any, error)
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type Event struct {
	ID            uint64 `json:"id"`
	Session, Type string
	Data          any       `json:"data"`
	At            time.Time `json:"at"`
}

type eventLog struct {
	mu        sync.Mutex
	next      uint64
	max       int
	events    []Event
	listeners map[chan Event]struct{}
}

func newLog(n int) *eventLog { return &eventLog{max: n, listeners: map[chan Event]struct{}{}} }
func (l *eventLog) add(e Event) Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	e.ID = l.next
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	l.events = append(l.events, e)
	if len(l.events) > l.max {
		l.events = l.events[len(l.events)-l.max:]
	}
	for c := range l.listeners {
		select {
		case c <- e:
		default:
		}
	}
	return e
}
func (l *eventLog) since(after uint64) ([]Event, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.events) > 0 && after+1 < l.events[0].ID {
		return nil, true
	}
	var out []Event
	for _, e := range l.events {
		if e.ID > after {
			out = append(out, e)
		}
	}
	return out, false
}
func (l *eventLog) sub() (<-chan Event, func()) {
	c := make(chan Event, 32)
	l.mu.Lock()
	l.listeners[c] = struct{}{}
	l.mu.Unlock()
	return c, func() {
		l.mu.Lock()
		if _, ok := l.listeners[c]; ok {
			delete(l.listeners, c)
			close(c)
		}
		l.mu.Unlock()
	}
}

type Bridge struct {
	verify    VerifyToken
	invoke    Invoker
	mu        sync.Mutex
	logs      map[string]*eventLog
	maxEvents int
}

func New(v VerifyToken, i Invoker) *Bridge {
	return &Bridge{verify: v, invoke: i, logs: map[string]*eventLog{}, maxEvents: 256}
}
func (b *Bridge) log(s string) *eventLog {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.logs[s] == nil {
		b.logs[s] = newLog(b.maxEvents)
	}
	return b.logs[s]
}
func (b *Bridge) Emit(s, t string, d any) Event {
	return b.log(s).add(Event{Session: s, Type: t, Data: d})
}
func (b *Bridge) auth(w http.ResponseWriter, r *http.Request) (Identity, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") || b.verify == nil {
		http.Error(w, "unauthorized", 401)
		return Identity{}, false
	}
	u, ok := b.verify(strings.TrimSpace(h[7:]))
	s := strings.TrimSpace(r.Header.Get("X-Session-ID"))
	if !ok || u == "" || s == "" || strings.ContainsAny(s, " \r\n\t") {
		http.Error(w, "unauthorized", 401)
		return Identity{}, false
	}
	return Identity{u, s}, true
}
func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/rpc":
		b.rpc(w, r)
	case "/events":
		b.sse(w, r)
	case "/ws":
		b.ws(w, r)
	default:
		http.NotFound(w, r)
	}
}
func out(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func (b *Bridge) rpc(w http.ResponseWriter, r *http.Request) {
	id, ok := b.auth(w, r)
	if !ok {
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var q Request
	if json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&q) != nil || q.JSONRPC != "2.0" {
		out(w, response{JSONRPC: "2.0", Error: &rpcError{-32600, "invalid request"}})
		return
	}
	if q.Method == "bridge.ping" {
		out(w, response{JSONRPC: "2.0", ID: q.ID, Result: id})
		return
	}
	if q.Method != "bridge.invoke" || b.invoke == nil {
		out(w, response{JSONRPC: "2.0", ID: q.ID, Error: &rpcError{-32601, "method not found"}})
		return
	}
	var p struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(q.Params, &p) != nil || p.Method == "" {
		out(w, response{JSONRPC: "2.0", ID: q.ID, Error: &rpcError{-32602, "invalid params"}})
		return
	}
	v, e := b.invoke(r.Context(), id, p.Method, p.Params)
	if e != nil {
		out(w, response{JSONRPC: "2.0", ID: q.ID, Error: &rpcError{-32000, "invocation failed"}})
		return
	}
	b.Emit(id.Session, "invocation.completed", map[string]string{"method": p.Method})
	out(w, response{JSONRPC: "2.0", ID: q.ID, Result: v})
}
func (b *Bridge) sse(w http.ResponseWriter, r *http.Request) {
	id, ok := b.auth(w, r)
	if !ok {
		return
	}
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}
	a, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
	replay, gap := b.log(id.Session).since(a)
	if gap {
		http.Error(w, "event cursor expired", 410)
		return
	}
	c, cancel := b.log(id.Session).sub()
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", 500)
		return
	}
	for _, e := range replay {
		event(w, e)
		f.Flush()
	}
	for {
		select {
		case e := <-c:
			event(w, e)
			f.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
func event(w io.Writer, e Event) {
	d, _ := json.Marshal(e.Data)
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, d)
}

func accept(k string) string {
	h := sha1.New()
	io.WriteString(h, k+"258EAFA5-E914-47DA-95CA-C5AB0DC85B11")
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
func (b *Bridge) ws(w http.ResponseWriter, r *http.Request) {
	id, ok := b.auth(w, r)
	if !ok {
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "upgrade required", 426)
		return
	}
	k := r.Header.Get("Sec-WebSocket-Key")
	h, ok := w.(http.Hijacker)
	if k == "" || !ok {
		http.Error(w, "bad websocket request", 400)
		return
	}
	c, _, e := h.Hijack()
	if e != nil {
		return
	}
	defer c.Close()
	fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept(k))
	for {
		p, e := readFrame(c)
		if e != nil {
			return
		}
		var q Request
		if json.Unmarshal(p, &q) != nil {
			writeFrame(c, must(response{JSONRPC: "2.0", Error: &rpcError{-32700, "parse error"}}))
			continue
		}
		if q.Method == "bridge.ping" {
			writeFrame(c, must(response{JSONRPC: "2.0", ID: q.ID, Result: id}))
			continue
		}
		writeFrame(c, must(response{JSONRPC: "2.0", ID: q.ID, Error: &rpcError{-32601, "method not found"}}))
	}
}
func must(v any) []byte { b, _ := json.Marshal(v); return b }
func readFrame(c net.Conn) ([]byte, error) {
	h := make([]byte, 2)
	if _, e := io.ReadFull(c, h); e != nil {
		return nil, e
	}
	if h[0]&15 != 1 || h[1]&128 == 0 {
		return nil, errors.New("invalid frame")
	}
	n := int64(h[1] & 127)
	if n == 126 {
		var x uint16
		if binary.Read(c, binary.BigEndian, &x) != nil {
			return nil, io.ErrUnexpectedEOF
		}
		n = int64(x)
	}
	if n > 4<<20 {
		return nil, errors.New("frame too large")
	}
	m := make([]byte, 4)
	io.ReadFull(c, m)
	p := make([]byte, n)
	io.ReadFull(c, p)
	for i := range p {
		p[i] ^= m[i%4]
	}
	return p, nil
}
func writeFrame(c net.Conn, p []byte) error {
	if len(p) > 125 {
		return errors.New("response too large")
	}
	_, e := c.Write(append([]byte{129, byte(len(p))}, p...))
	return e
}
func ConstantTimeTokenEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (b *Bridge) ServeControlSocket(ctx context.Context, path string) error {
	l, e := net.Listen("unix", path)
	if e != nil {
		return e
	}
	defer l.Close()
	go func() { <-ctx.Done(); l.Close() }()
	for {
		c, e := l.Accept()
		if e != nil {
			return e
		}
		go b.control(c)
	}
}
func (b *Bridge) control(c net.Conn) {
	defer c.Close()
	s := bufio.NewScanner(io.LimitReader(c, 8<<20))
	for s.Scan() {
		var q struct {
			Token, Session string
			Request
		}
		if json.Unmarshal(s.Bytes(), &q) != nil {
			continue
		}
		u, ok := b.verify(q.Token)
		if !ok || u == "" || q.Session == "" {
			continue
		}
		outLine(c, response{JSONRPC: "2.0", ID: q.ID, Result: Identity{u, q.Session}})
	}
}
func outLine(c net.Conn, v any) { b, _ := json.Marshal(v); c.Write(append(b, '\n')) }
