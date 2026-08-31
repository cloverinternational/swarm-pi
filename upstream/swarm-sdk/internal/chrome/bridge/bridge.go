// Package bridge implements the authenticated loopback transport between the
// Chrome extension and the runtime manager.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome/protocol"
	"github.com/gorilla/websocket"
)

var (
	ErrClosed       = errors.New("chrome bridge: closed")
	ErrNotConnected = errors.New("chrome bridge: no authenticated connection")
	ErrQueueFull    = errors.New("chrome bridge: outbound queue full")
	ErrPendingFull  = errors.New("chrome bridge: pending request bound reached")
	ErrDuplicate    = errors.New("chrome bridge: duplicate pending request")
	ErrStaleBinding = errors.New("chrome bridge: stale family capability")
	ErrEventGap     = errors.New("chrome bridge: event sequence gap")
)

type Config struct {
	ExtensionID          string
	InstallationID       string
	InstallationSecret   []byte
	ManagerEpoch         uint64
	ManagerInstanceID    string
	Supported            protocol.Advertisement
	RequiredCapabilities []string
	QueueSize            int
	PendingLimit         int
	HistoryLimit         int
	MaxHandshakes        int
	MaxSnapshotRecords   int
	MaxReconcileEntries  int
	HandshakeTimeout     time.Duration
	ReadTimeout          time.Duration
	WriteTimeout         time.Duration
	PingInterval         time.Duration
	OnControl            func(protocol.Control)
	OnEvent              func(protocol.Event)
	OnEventGap           func(EventGap)
	OnDisconnect         func(Disconnect)
	// AuthorizeEvent resolves an event to its current claim and capability.
	// It is called without internal locks. If nil, generation must identify
	// exactly one current binding.
	AuthorizeEvent func(protocol.Event) (claimID, capability string, ok bool)
}

type Key struct {
	ClaimID    string
	Generation uint64
	RequestID  string
}

type DeliveryState string

const (
	DeliveryCreated      DeliveryState = "created"
	DeliveryQueued       DeliveryState = "queued"
	DeliverySent         DeliveryState = "sent"
	DeliveryAccepted     DeliveryState = "accepted"
	DeliveryStarted      DeliveryState = "started"
	DeliveryCompleted    DeliveryState = "completed"
	DeliveryRejected     DeliveryState = "rejected"
	DeliveryFailed       DeliveryState = "failed"
	DeliveryDisconnected DeliveryState = "disconnected"
)

type Delivery struct {
	Key         Key
	Operation   string
	State       DeliveryState
	Dispatched  bool
	SessionID   string
	LastUpdated time.Time
}

type Result struct {
	Response protocol.Response
	Err      error
}

type EventGap struct {
	SessionID  string
	ClaimID    string
	Generation uint64
	Expected   uint64
	Received   uint64
}

type Disconnect struct {
	SessionID string
	Pending   []Delivery
}

type bindingKey struct {
	claimID    string
	generation uint64
}

type pending struct {
	request protocol.Request
	key     Key
	session string
	state   DeliveryState
	sent    bool
	updated time.Time
	result  chan Result
}

type outbound struct {
	message any
	key     *Key
}

type connection struct {
	id             string
	ws             *websocket.Conn
	send           chan outbound
	done           chan struct{}
	once           sync.Once
	disconnectOnce sync.Once
}

func (c *connection) stop() {
	c.once.Do(func() {
		close(c.done)
		_ = c.ws.Close()
	})
}

type Server struct {
	cfg      Config
	upgrader websocket.Upgrader

	mu           sync.Mutex
	listener     net.Listener
	http         *http.Server
	conn         *connection
	pending      map[Key]*pending
	history      map[Key]Delivery
	historyOrder []Key
	bindings     map[bindingKey]string
	sequence     map[bindingKey]uint64
	handshakes   chan struct{}
	closed       bool
	wg           sync.WaitGroup
}

func New(cfg Config) (*Server, error) {
	if cfg.ExtensionID == "" || cfg.InstallationID == "" || len(cfg.InstallationSecret) < 32 ||
		len(cfg.InstallationSecret) > 4096 || cfg.ManagerEpoch == 0 ||
		cfg.ManagerEpoch > protocol.MaxSafeInteger || cfg.ManagerInstanceID == "" ||
		len(cfg.ExtensionID) > protocol.MaxIdentifierBytes ||
		len(cfg.InstallationID) > protocol.MaxIdentifierBytes ||
		len(cfg.ManagerInstanceID) > protocol.MaxIdentifierBytes ||
		len(cfg.RequiredCapabilities) > protocol.MaxCapabilities ||
		!stringsBounded(cfg.RequiredCapabilities) ||
		len(cfg.Supported.RPC) == 0 || len(cfg.Supported.Events) == 0 ||
		len(cfg.Supported.RPC) > protocol.MaxListEntries ||
		len(cfg.Supported.Events) > protocol.MaxListEntries ||
		!rangesValid(cfg.Supported.RPC) || !rangesValid(cfg.Supported.Events) ||
		len(cfg.Supported.Capabilities) > protocol.MaxCapabilities ||
		!stringsBounded(cfg.Supported.Capabilities) {
		return nil, fmt.Errorf("chrome bridge: invalid authentication configuration")
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 64
	}
	if cfg.PendingLimit <= 0 {
		cfg.PendingLimit = 256
	}
	if cfg.HistoryLimit <= 0 {
		cfg.HistoryLimit = 1024
	}
	if cfg.MaxHandshakes <= 0 {
		cfg.MaxHandshakes = 4
	}
	if cfg.MaxSnapshotRecords <= 0 {
		cfg.MaxSnapshotRecords = 256
	}
	if cfg.MaxReconcileEntries <= 0 {
		cfg.MaxReconcileEntries = 256
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = 5 * time.Second
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 30 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 5 * time.Second
	}
	if cfg.QueueSize > 4096 || cfg.PendingLimit > 4096 || cfg.HistoryLimit > 16384 ||
		cfg.MaxHandshakes > 64 || cfg.MaxSnapshotRecords > protocol.MaxListEntries ||
		cfg.MaxReconcileEntries > protocol.MaxListEntries {
		return nil, fmt.Errorf("chrome bridge: configuration exceeds bounds")
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = cfg.ReadTimeout / 2
	}
	cfg.InstallationSecret = append([]byte(nil), cfg.InstallationSecret...)
	cfg.RequiredCapabilities = append([]string(nil), cfg.RequiredCapabilities...)
	cfg.Supported.RPC = append([]protocol.VersionRange(nil), cfg.Supported.RPC...)
	cfg.Supported.Events = append([]protocol.VersionRange(nil), cfg.Supported.Events...)
	cfg.Supported.Capabilities = append([]string(nil), cfg.Supported.Capabilities...)
	s := &Server{
		cfg:        cfg,
		pending:    make(map[Key]*pending),
		history:    make(map[Key]Delivery),
		bindings:   make(map[bindingKey]string),
		sequence:   make(map[bindingKey]uint64),
		handshakes: make(chan struct{}, cfg.MaxHandshakes),
	}
	s.upgrader.CheckOrigin = s.checkOrigin
	s.upgrader.ReadBufferSize = 4096
	s.upgrader.WriteBufferSize = 4096
	s.upgrader.EnableCompression = false
	return s, nil
}
func stringsBounded(values []string) bool {
	for _, value := range values {
		if value == "" || len(value) > protocol.MaxIdentifierBytes {
			return false
		}
	}
	return true
}

// Listen starts serving on an already-bound loopback listener.
func rangesValid(values []protocol.VersionRange) bool {
	for _, value := range values {
		if !value.Valid() {
			return false
		}
	}
	return true
}
func (s *Server) Listen(listener net.Listener) error {
	if !isIPv4Localhost(listener.Addr()) {
		return fmt.Errorf("chrome bridge: listener must be 127.0.0.1")
	}
	s.mu.Lock()
	if s.listener != nil || s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.listener = listener
	mux := http.NewServeMux()
	mux.HandleFunc("/chrome/bridge", s.handleWebSocket)
	s.http = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: s.cfg.HandshakeTimeout,
		ReadTimeout:       s.cfg.ReadTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		MaxHeaderBytes:    16 << 10,
	}
	server := s.http
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		_ = server.Serve(listener)
	}()
	return nil
}

func (s *Server) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return "ws://" + s.listener.Addr().String() + "/chrome/bridge"
}

func (s *Server) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conn := s.conn
	server := s.http
	s.mu.Unlock()
	if conn != nil {
		conn.stop()
	}
	var err error
	if server != nil {
		err = server.Shutdown(ctx)
	}
	s.wg.Wait()
	return err
}

func (s *Server) checkOrigin(r *http.Request) bool {
	return r.Header.Get("Origin") == "chrome-extension://"+s.cfg.ExtensionID
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	listener := s.listener
	closed := s.closed
	s.mu.Unlock()
	if closed || listener == nil || r.Method != http.MethodGet || r.Host != listener.Addr().String() ||
		r.URL.RawQuery != "" || r.URL.User != nil || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" ||
		!s.checkOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	select {
	case s.handshakes <- struct{}{}:
		defer func() { <-s.handshakes }()
	default:
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(protocol.MaxMessageBytes)
	conn, negotiated, err := s.authenticate(ws)
	if err != nil {
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"), time.Now().Add(s.cfg.WriteTimeout))
		_ = ws.Close()
		return
	}
	if err := s.writeJSON(ws, negotiated); err != nil {
		_ = ws.Close()
		return
	}
	s.install(conn)
}

func (s *Server) authenticate(ws *websocket.Conn) (*connection, protocol.NegotiatedControl, error) {
	_ = ws.SetReadDeadline(time.Now().Add(s.cfg.HandshakeTimeout))
	_, raw, err := ws.ReadMessage()
	if err != nil {
		return nil, protocol.NegotiatedControl{}, err
	}
	control, err := protocol.DecodeControl(raw)
	if err != nil {
		return nil, protocol.NegotiatedControl{}, err
	}
	hello, ok := control.(*protocol.AuthHello)
	if !ok || hello.InstallationID != s.cfg.InstallationID {
		return nil, protocol.NegotiatedControl{}, errors.New("unexpected installation")
	}
	selected, protocolErr := protocol.Negotiate(s.cfg.Supported, hello.Supported, s.cfg.RequiredCapabilities)
	if protocolErr != nil {
		return nil, protocol.NegotiatedControl{}, protocolErr
	}
	serverNonce, err := randomToken()
	if err != nil {
		return nil, protocol.NegotiatedControl{}, err
	}
	challenge := protocol.AuthChallenge{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlAuthChallenge,
		InstallationID: s.cfg.InstallationID, ClientNonce: hello.ClientNonce, ServerNonce: serverNonce,
		ManagerEpoch: s.cfg.ManagerEpoch, ManagerInstanceID: s.cfg.ManagerInstanceID,
		Selected: selected,
	}
	challenge.ServerProof = handshakeProof(s.cfg.InstallationSecret, "server", challenge.InstallationID,
		challenge.ClientNonce, challenge.ServerNonce, challenge.ManagerEpoch, challenge.ManagerInstanceID, challenge.Selected)
	if err := s.writeJSON(ws, challenge); err != nil {
		return nil, protocol.NegotiatedControl{}, err
	}
	_, raw, err = ws.ReadMessage()
	if err != nil {
		return nil, protocol.NegotiatedControl{}, err
	}
	control, err = protocol.DecodeControl(raw)
	if err != nil {
		return nil, protocol.NegotiatedControl{}, err
	}
	proof, ok := control.(*protocol.AuthProof)
	if !ok || proof.InstallationID != challenge.InstallationID || proof.ClientNonce != challenge.ClientNonce ||
		proof.ServerNonce != challenge.ServerNonce || proof.ManagerEpoch != challenge.ManagerEpoch ||
		proof.ManagerInstanceID != challenge.ManagerInstanceID || !negotiatedEqual(proof.Selected, challenge.Selected) {
		return nil, protocol.NegotiatedControl{}, errors.New("challenge binding mismatch")
	}
	expected := handshakeProof(s.cfg.InstallationSecret, "client", proof.InstallationID,
		proof.ClientNonce, proof.ServerNonce, proof.ManagerEpoch, proof.ManagerInstanceID, proof.Selected)
	if !secureEqual(expected, proof.ClientProof) {
		return nil, protocol.NegotiatedControl{}, errors.New("invalid client proof")
	}
	session, err := randomToken()
	if err != nil {
		return nil, protocol.NegotiatedControl{}, err
	}
	_ = ws.SetReadDeadline(time.Time{})
	conn := &connection{id: session, ws: ws, send: make(chan outbound, s.cfg.QueueSize), done: make(chan struct{})}
	return conn, protocol.NegotiatedControl{
		Contract: protocol.ControlContractV1, Kind: protocol.ControlNegotiated, SessionID: session,
		ManagerEpoch: s.cfg.ManagerEpoch, ManagerInstanceID: s.cfg.ManagerInstanceID, Selected: selected,
	}, nil
}

func negotiatedEqual(left, right protocol.Negotiated) bool {
	if left.RPC != right.RPC || left.Events != right.Events || len(left.Capabilities) != len(right.Capabilities) {
		return false
	}
	for index := range left.Capabilities {
		if left.Capabilities[index] != right.Capabilities[index] {
			return false
		}
	}
	return true
}
func (s *Server) install(conn *connection) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		conn.stop()
		return
	}
	old := s.conn
	s.conn = conn
	s.bindings = make(map[bindingKey]string)
	s.sequence = make(map[bindingKey]uint64)
	s.mu.Unlock()
	if old != nil {
		old.stop()
	}
	s.wg.Add(2)
	go s.writeLoop(conn)
	go s.readLoop(conn)
}

func (s *Server) writeJSON(ws *websocket.Conn, message any) error {
	raw, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if len(raw) > protocol.MaxMessageBytes {
		return fmt.Errorf("chrome bridge: outbound message exceeds %d bytes", protocol.MaxMessageBytes)
	}
	_ = ws.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout))
	return ws.WriteMessage(websocket.TextMessage, raw)
}

func (s *Server) writeLoop(conn *connection) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.cfg.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-conn.done:
			return
		case item := <-conn.send:
			if item.key != nil {
				s.markSent(conn.id, *item.key)
			}
			if err := s.writeJSON(conn.ws, item.message); err != nil {
				s.disconnect(conn)
				return
			}
		case <-ticker.C:
			if err := conn.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(s.cfg.WriteTimeout)); err != nil {
				s.disconnect(conn)
				return
			}
		}
	}
}

func (s *Server) readLoop(conn *connection) {
	defer s.wg.Done()
	defer s.disconnect(conn)
	conn.ws.SetPongHandler(func(string) error {
		return conn.ws.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))
	})
	for {
		_ = conn.ws.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))
		messageType, raw, err := conn.ws.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage || len(raw) > protocol.MaxMessageBytes {
			return
		}
		var discriminator struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &discriminator) != nil {
			return
		}
		switch discriminator.Kind {
		case "response", "event":
			message, err := protocol.DecodeMessage(raw)
			if err != nil {
				return
			}
			if response, ok := message.(*protocol.Response); ok {
				s.handleResponse(conn, response)
			} else {
				s.handleEvent(conn, message.(*protocol.Event))
			}
		default:
			control, err := protocol.DecodeControl(raw)
			if err != nil || !protocol.ValidInboundControl(protocol.ControlKind(control)) ||
				!s.controlWithinBounds(control) {
				return
			}
			s.handleControl(conn, control)
		}
	}
}

func (s *Server) disconnect(conn *connection) {
	conn.disconnectOnce.Do(func() {
		conn.stop()
		var callback func(Disconnect)
		var notice Disconnect
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
		}
		notice.SessionID = conn.id
		for _, pending := range s.pending {
			if pending.session != conn.id {
				continue
			}
			pending.state = DeliveryDisconnected
			pending.updated = time.Now()
			delivery := deliveryOf(pending)
			s.saveHistoryLocked(delivery)
			notice.Pending = append(notice.Pending, delivery)
			select {
			case pending.result <- Result{Err: ErrClosed}:
			default:
			}
			delete(s.pending, pending.key)
		}
		callback = s.cfg.OnDisconnect
		s.mu.Unlock()
		if callback != nil {
			callback(notice)
		}
	})
}

func (s *Server) Bind(claimID string, generation uint64, capability string) error {
	if claimID == "" || generation == 0 || capability == "" {
		return ErrStaleBinding
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return ErrNotConnected
	}
	s.bindings[bindingKey{claimID, generation}] = capability
	return nil
}

func (s *Server) Revoke(claimID string, generation uint64) {
	s.mu.Lock()
	delete(s.bindings, bindingKey{claimID, generation})
	delete(s.sequence, bindingKey{claimID, generation})
	s.mu.Unlock()
}

func (s *Server) SendControl(message protocol.Control) error {
	if err := protocol.ValidateControl(message); err != nil {
		return err
	}
	if !protocol.ValidOutboundControl(protocol.ControlKind(message)) ||
		!s.controlWithinBounds(message) || !protocol.EncodedSizeOK(message) {
		return ErrQueueFull
	}
	return s.enqueue(outbound{message: message})
}

func (s *Server) Dispatch(ctx context.Context, claimID string, request protocol.Request) (protocol.Response, error) {
	key := Key{ClaimID: claimID, Generation: request.Generation, RequestID: request.RequestID}
	now := time.Now()
	if err := protocol.ValidateMessage(&request); err != nil {
		return protocol.Response{}, err
	}
	if !protocol.EncodedSizeOK(request) {
		return protocol.Response{}, ErrQueueFull
	}
	if deadline := time.UnixMilli(request.DeadlineUnixMS); !deadline.After(now) {
		return protocol.Response{}, context.DeadlineExceeded
	}
	s.mu.Lock()
	conn := s.conn
	if conn == nil {
		s.mu.Unlock()
		return protocol.Response{}, ErrNotConnected
	}
	if s.bindings[bindingKey{claimID, request.Generation}] != request.FamilyCapability {
		s.mu.Unlock()
		return protocol.Response{}, ErrStaleBinding
	}
	if len(s.pending) >= s.cfg.PendingLimit {
		s.mu.Unlock()
		return protocol.Response{}, ErrPendingFull
	}
	if _, exists := s.pending[key]; exists {
		s.mu.Unlock()
		return protocol.Response{}, ErrDuplicate
	}
	p := &pending{request: request, key: key, session: conn.id, state: DeliveryCreated, updated: now, result: make(chan Result, 1)}
	s.pending[key] = p
	select {
	case conn.send <- outbound{message: request, key: &key}:
		p.state = DeliveryQueued
		p.updated = time.Now()
	default:
		delete(s.pending, key)
		s.mu.Unlock()
		return protocol.Response{}, ErrQueueFull
	}
	s.mu.Unlock()

	timer := time.NewTimer(time.Until(time.UnixMilli(request.DeadlineUnixMS)))
	defer timer.Stop()
	select {
	case result := <-p.result:
		return result.Response, result.Err
	case <-ctx.Done():
		s.removePending(key, p)
		return protocol.Response{}, ctx.Err()
	case <-timer.C:
		s.removePending(key, p)
		return protocol.Response{}, context.DeadlineExceeded
	}
}

func (s *Server) removePending(key Key, expected *pending) {
	s.mu.Lock()
	if s.pending[key] == expected {
		s.saveHistoryLocked(deliveryOf(expected))
		delete(s.pending, key)
	}
	s.mu.Unlock()
}

func (s *Server) enqueue(item outbound) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return ErrNotConnected
	}
	select {
	case s.conn.send <- item:
		return nil
	default:
		return ErrQueueFull
	}
}

func (s *Server) markSent(session string, key Key) {
	s.mu.Lock()
	if pending := s.pending[key]; pending != nil && pending.session == session {
		pending.state, pending.sent, pending.updated = DeliverySent, true, time.Now()
	}
	s.mu.Unlock()
}

func (s *Server) handleResponse(conn *connection, response *protocol.Response) {
	key := Key{ClaimID: response.ClaimID, Generation: response.Generation, RequestID: response.RequestID}
	s.mu.Lock()
	p := s.pending[key]
	if p == nil || p.session != conn.id || protocol.ValidateResponseCorrelation(*response, p.request, key.ClaimID) != nil ||
		s.bindings[bindingKey{key.ClaimID, key.Generation}] != p.request.FamilyCapability {
		s.mu.Unlock()
		return
	}
	switch response.Terminal {
	case protocol.TerminalCompleted:
		p.state = DeliveryCompleted
	case protocol.TerminalRejected:
		p.state = DeliveryRejected
	default:
		p.state = DeliveryFailed
	}
	p.updated = time.Now()
	s.saveHistoryLocked(deliveryOf(p))
	delete(s.pending, key)
	s.mu.Unlock()
	p.result <- Result{Response: *response}
}

func (s *Server) handleEvent(conn *connection, event *protocol.Event) {
	var callback func(protocol.Event)
	var gapCallback func(EventGap)
	var gap *EventGap
	var resolvedClaim, resolvedCapability string
	var resolved bool
	if s.cfg.AuthorizeEvent != nil {
		resolvedClaim, resolvedCapability, resolved = s.cfg.AuthorizeEvent(*event)
		if !resolved {
			return
		}
	}
	s.mu.Lock()
	if s.conn != conn {
		s.mu.Unlock()
		return
	}
	if event.RequestID != "" {
		var matched *pending
		for _, p := range s.pending {
			if p.session == conn.id && p.key.Generation == event.Generation && p.key.RequestID == event.RequestID &&
				(!resolved || p.key.ClaimID == resolvedClaim) {
				if matched != nil {
					s.mu.Unlock()
					return
				}
				matched = p
			}
		}
		if matched == nil {
			s.mu.Unlock()
			return
		}
		if event.Event == "request_accepted" {
			matched.state = DeliveryAccepted
		} else if event.Event == "request_started" {
			matched.state = DeliveryStarted
		}
		matched.updated = time.Now()
	}
	var key bindingKey
	if resolved {
		key = bindingKey{resolvedClaim, event.Generation}
		if s.bindings[key] != resolvedCapability {
			s.mu.Unlock()
			return
		}
	} else {
		found := false
		for candidate := range s.bindings {
			if candidate.generation == event.Generation {
				if found {
					s.mu.Unlock()
					return
				}
				key, found = candidate, true
			}
		}
		if !found {
			s.mu.Unlock()
			return
		}
	}
	last := s.sequence[key]
	if event.Sequence <= last {
		s.mu.Unlock()
		return
	}
	if last != 0 && event.Sequence != last+1 {
		value := EventGap{SessionID: conn.id, ClaimID: key.claimID, Generation: event.Generation, Expected: last + 1, Received: event.Sequence}
		gap = &value
		gapCallback = s.cfg.OnEventGap
	}
	s.sequence[key] = event.Sequence
	callback = s.cfg.OnEvent
	s.mu.Unlock()
	if gap != nil && gapCallback != nil {
		gapCallback(*gap)
	}
	if callback != nil {
		callback(*event)
	}
}

func (s *Server) handleControl(conn *connection, control protocol.Control) {
	if !s.controlWithinBounds(control) {
		return
	}
	s.mu.Lock()
	current := s.conn == conn
	callback := s.cfg.OnControl
	s.mu.Unlock()
	if current && callback != nil {
		callback(control)
	}
}

func (s *Server) Delivery(key Key) (Delivery, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pending := s.pending[key]; pending != nil {
		return deliveryOf(pending), true
	}
	delivery, ok := s.history[key]
	return delivery, ok
}

func (s *Server) saveHistoryLocked(delivery Delivery) {
	if _, exists := s.history[delivery.Key]; !exists {
		s.historyOrder = append(s.historyOrder, delivery.Key)
	}
	s.history[delivery.Key] = delivery
	for len(s.historyOrder) > s.cfg.HistoryLimit {
		oldest := s.historyOrder[0]
		s.historyOrder = s.historyOrder[1:]
		delete(s.history, oldest)
	}
}

func (s *Server) Pending() []Delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Delivery, 0, len(s.pending))
	for _, pending := range s.pending {
		result = append(result, deliveryOf(pending))
	}
	return result
}

func deliveryOf(p *pending) Delivery {
	return Delivery{
		Key: p.key, Operation: p.request.Operation, State: p.state, Dispatched: p.sent,
		SessionID: p.session, LastUpdated: p.updated,
	}
}

func isIPv4Localhost(addr net.Addr) bool {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.String() == "127.0.0.1"
}

// VerifyCapabilityProof validates a rebind proof against the installation secret.
func (s *Server) VerifyCapabilityProof(proof protocol.CapabilityProof) bool {
	if proof.InstallationID != s.cfg.InstallationID {
		return false
	}
	expected := rebindProof(s.cfg.InstallationSecret, proof.InstallationID, proof.ClaimID,
		proof.Generation, proof.WindowID, proof.Challenge)
	return secureEqual(expected, proof.Proof)
}

func (s *Server) controlWithinBounds(control protocol.Control) bool {
	switch value := control.(type) {
	case *protocol.OwnershipSnapshot:
		return len(value.Records) <= s.cfg.MaxSnapshotRecords
	case *protocol.OwnershipReconcile:
		return len(value.LiveLaunchIDs) <= s.cfg.MaxSnapshotRecords &&
			len(value.Pending) <= s.cfg.MaxSnapshotRecords &&
			len(value.Committed) <= s.cfg.MaxSnapshotRecords &&
			len(value.Authorized) <= s.cfg.MaxSnapshotRecords
	case *protocol.ReconcileRequest:
		return len(value.RequestIDs) <= s.cfg.MaxReconcileEntries
	case *protocol.ReconcileResult:
		return len(value.Entries) <= s.cfg.MaxReconcileEntries
	default:
		return true
	}
}
