package termimage

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

var processIDs atomic.Uint32

const maxRetainedImages = 64

func init() {
	processIDs.Store(0x40000000 | (uint32(os.Getpid())&0x3fff)<<16)
}

type sourceState struct {
	source   Source
	imageID  uint32
	retries  int
	err      string
	lastUsed uint64
	// uploaded is the set of Kitty image IDs currently transmitted for this
	// source, not a single flag. A source that is evicted and re-rendered is
	// re-registered under a fresh ID while already-rendered placeholder cells
	// still encode the previous one, so one source can legitimately need two
	// live IDs at once. Tracking a set is what lets every placement in a frame
	// be backed by pixels the terminal actually holds.
	uploaded map[uint32]bool
	// transfers records every temp-file path ever written for this source,
	// keyed by image ID. Invalidate clears `uploaded` (forcing retransmission)
	// but must not lose the paths, or eviction has nothing left to clean up and
	// the files sit in TMPDIR until the 24h sweeper runs.
	transfers map[uint32]string
}

// maxUploadsPerSource bounds the per-source image-ID set so a very long session
// that repeatedly evicts and re-registers the same image cannot grow it without
// limit. Stale IDs beyond this are deleted from the terminal.
const maxUploadsPerSource = 4

func (s *sourceState) isUploaded(id uint32) bool { return s.uploaded[id] }

func (s *sourceState) trackTransfer(id uint32, path string) {
	if s.transfers == nil {
		s.transfers = make(map[uint32]string, 1)
	}
	s.transfers[id] = path
}

func (s *sourceState) markUploaded(id uint32) {
	if s.uploaded == nil {
		s.uploaded = make(map[uint32]bool, 1)
	}
	s.uploaded[id] = true
}

type placementState struct {
	placement Placement
	created   bool
}

// Manager owns Kitty image IDs, probes the available transport, and reconciles
// terminal resources before synchronized text frames are written.
type Manager struct {
	mu                sync.Mutex
	capability        Capability
	appliedCapability Capability
	transport         Transport
	envelope          Envelope
	desired           Frame
	applied           Frame
	dirty             bool
	sources           map[string]*sourceState
	placements        map[string]*placementState
	probeFiles        map[uint32]string
	transferFiles     map[uint32]string
	clock             uint64
}

func NewManager() *Manager {
	cleanupStaleTempFiles()
	m := &Manager{
		sources:       make(map[string]*sourceState),
		placements:    make(map[string]*placementState),
		probeFiles:    make(map[uint32]string),
		transferFiles: make(map[uint32]string),
	}
	if c, ok := CapabilityFromEnv(); ok {
		m.capability = c
	}
	return m
}

func (m *Manager) Capability() Capability {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.capability
}

func (m *Manager) Transport() Transport {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.transport
}

func (m *Manager) Envelope() Envelope {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.envelope
}

func (m *Manager) SetCapability(c Capability) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.capability != c {
		m.capability = c
		m.dirty = true
	}
}

// Register returns stable Kitty identities for one image occurrence.
func (m *Manager) Register(source Source, occurrence string, columns, rows int) (Placement, error) {
	if source.Key == "" || len(source.PNG) == 0 || source.Width <= 0 || source.Height <= 0 {
		return Placement{}, fmt.Errorf("termimage: invalid source")
	}
	if columns <= 0 || rows <= 0 {
		return Placement{}, fmt.Errorf("termimage: invalid placement %dx%d", columns, rows)
	}
	if occurrence == "" {
		occurrence = source.Key
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.clock++
	state := m.sources[source.Key]
	if state == nil {
		state = &sourceState{source: source, imageID: m.allocateID(), lastUsed: m.clock}
		m.sources[source.Key] = state
	} else {
		state.lastUsed = m.clock
	}
	key := placementKey(source.Key, occurrence, columns, rows)
	if existing := m.placements[key]; existing != nil {
		return existing.placement, nil
	}
	placement := Placement{
		Source: source, Occurrence: occurrence, Columns: columns, Rows: rows,
		ImageID: state.imageID, PlacementID: m.allocatePlacementID(),
	}
	m.placements[key] = &placementState{placement: placement}
	m.dirty = true
	return placement, nil
}

func (m *Manager) Publish(frame Frame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	frame = cloneFrame(frame)
	if !framesEqual(m.desired, frame) {
		m.desired = frame
		m.dirty = true
	}
}

func (m *Manager) Desired() Frame {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneFrame(m.desired)
}

func (m *Manager) Applied() Frame {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneFrame(m.applied)
}

// ProbeCommands returns capability queries and closed temp-file queries. Under
// tmux both native/raw and documented DCS-passthrough routes are tested.
func (m *Manager) ProbeCommands(inTmux bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out stringsBuilder
	out.WriteString(directQuery(queryDirectID))
	path, err := m.createProbeFile(queryTempID)
	if err == nil {
		out.WriteString(tempQuery(queryTempID, path))
	}
	if inTmux {
		out.WriteString(WrapTmuxPassthrough(directQuery(queryTmuxID)))
		path, tempErr := m.createProbeFile(queryTmuxTempID)
		if tempErr == nil {
			out.WriteString(WrapTmuxPassthrough(tempQuery(queryTmuxTempID, path)))
		}
		if err == nil {
			err = tempErr
		}
	}
	return out.String(), err
}

func (m *Manager) createProbeFile(id uint32) (string, error) {
	if previous := m.probeFiles[id]; previous != "" {
		_ = os.Remove(previous)
		delete(m.probeFiles, id)
	}
	f, err := os.CreateTemp("", "swarm-tty-graphics-protocol-probe-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err = f.Write([]byte{0, 0, 0}); err == nil {
		err = f.Close()
	} else {
		_ = f.Close()
	}
	if err != nil {
		_ = os.Remove(name)
		return "", err
	}
	m.probeFiles[id] = name
	return name, nil
}

// ObserveResponse applies probe results and upload failures. It returns true
// when image-bearing messages should be invalidated and rendered again.
func (m *Manager) ObserveResponse(response Response) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if path := m.probeFiles[response.ID]; path != "" {
		_ = os.Remove(path)
		delete(m.probeFiles, response.ID)
	}
	if path := m.transferFiles[response.ID]; path != "" {
		_ = os.Remove(path)
		delete(m.transferFiles, response.ID)
		for _, state := range m.sources {
			delete(state.transfers, response.ID)
		}
	}
	switch response.ID {
	case queryDirectID:
		if response.OK {
			if m.capability == Kitty && m.envelope == Raw && m.transport == TemporaryFile {
				return false
			}
			return m.selectProbe(Raw, Direct)
		}
		return false
	case queryTmuxID:
		if response.OK && m.envelope != Raw {
			if m.capability == Kitty && m.envelope == TmuxPassthrough && m.transport == TemporaryFile {
				return false
			}
			return m.selectProbe(TmuxPassthrough, Direct)
		}
		return false
	case queryTempID:
		if response.OK {
			return m.selectProbe(Raw, TemporaryFile)
		}
		return false
	case queryTmuxTempID:
		if response.OK && m.envelope != Raw {
			return m.selectProbe(TmuxPassthrough, TemporaryFile)
		}
		return false
	}
	for _, state := range m.sources {
		// Match any live upload ID for the source, not just the newest one: a
		// rejection can name a previous ID that on-screen placeholders still
		// reference, and ignoring it would leave those cells permanently blank.
		if response.OK || (state.imageID != response.ID && !state.isUploaded(response.ID)) {
			continue
		}
		if m.transport == TemporaryFile {
			m.transport = Direct
			delete(state.uploaded, response.ID)
			state.retries = 0
			m.dirty = true
			return true
		}
		state.retries++
		delete(state.uploaded, response.ID)
		if state.retries > 1 {
			state.err = sanitizeProtocolError(response.Message)
		}
		m.dirty = true
		return true
	}
	return false
}

func (m *Manager) selectProbe(envelope Envelope, transport Transport) bool {
	changed := m.capability != Kitty || m.envelope != envelope || m.transport != transport
	m.capability, m.envelope, m.transport = Kitty, envelope, transport
	if changed {
		m.invalidateLocked()
	}
	return changed
}

// FinishProbe removes unconsumed probe files and resolves no-response startup.
func (m *Manager) FinishProbe() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, path := range m.probeFiles {
		_ = os.Remove(path)
		delete(m.probeFiles, id)
	}
	if m.capability == Unknown {
		m.capability = Unsupported
		m.dirty = true
		return true
	}
	return false
}

func (m *Manager) SourceError(key string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state := m.sources[key]; state != nil {
		return state.err
	}
	return ""
}

func (m *Manager) Invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invalidateLocked()
}

func (m *Manager) invalidateLocked() {
	for _, state := range m.sources {
		state.uploaded = nil
		state.err = ""
		state.retries = 0
	}
	for _, placement := range m.placements {
		placement.created = false
	}
	m.dirty = true
}

func (m *Manager) Reconcile(w io.Writer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.dirty {
		return nil
	}
	if m.capability != Kitty {
		m.applied = cloneFrame(m.desired)
		m.appliedCapability = m.capability
		m.dirty = false
		return nil
	}

	var command bytes.Buffer
	var tempFiles []string
	flushCommand := func() error {
		if command.Len() == 0 {
			return nil
		}
		if err := writeAll(w, command.Bytes()); err != nil {
			m.invalidateLocked()
			return err
		}
		command.Reset()
		return nil
	}
	visible := make(map[string]bool, len(m.desired.Placements))
	for _, placement := range m.desired.Placements {
		state := m.sources[placement.Source.Key]
		if state == nil && len(placement.Source.PNG) > 0 {
			state = &sourceState{
				source: placement.Source, imageID: placement.ImageID, lastUsed: m.clock,
			}
			m.sources[placement.Source.Key] = state
		}
		if state == nil || state.err != "" {
			continue
		}
		// The placeholder cells already written into the text frame encode
		// placement.ImageID. If the source was evicted and re-registered it now
		// carries a freshly minted ID, so uploading only under state.imageID
		// while creating the virtual placement under placement.ImageID would
		// put a placement on an image the terminal never received: it reserves
		// the rows and paints nothing — "the image is blank but the space where
		// it should be is there". Upload under the ID the frame actually asks
		// for, and keep the previous IDs alive for placeholders still on screen.
		imageID := placement.ImageID
		if imageID == 0 {
			imageID = state.imageID
		}
		visible[placement.Source.Key] = true
		m.clock++
		state.lastUsed = m.clock
		if !state.isUploaded(imageID) {
			m.pruneUploadsLocked(&command, state, imageID)
			// Do not accumulate an entire image-bearing frame in memory before
			// touching the terminal. Flush resource deletes, then stream the
			// upload in Kitty-sized writes. A frame containing several large
			// images otherwise creates a second, potentially hundreds-of-MB
			// command buffer on the renderer goroutine.
			if err := flushCommand(); err != nil {
				for _, pending := range tempFiles {
					_ = os.Remove(pending)
				}
				return err
			}
			path, err := writeUpload(w, state.source, imageID, m.transport, m.envelope)
			if err != nil {
				for _, pending := range tempFiles {
					_ = os.Remove(pending)
				}
				m.invalidateLocked()
				return err
			}
			if path != "" {
				tempFiles = append(tempFiles, path)
				if previous := state.transfers[imageID]; previous != "" && previous != path {
					// A retransmission under the same ID orphans the old file.
					_ = os.Remove(previous)
				}
				m.transferFiles[imageID] = path
				state.trackTransfer(imageID, path)
			}
			state.markUploaded(imageID)
		}
		key := placementKey(placement.Source.Key, placement.Occurrence, placement.Columns, placement.Rows)
		pstate := m.placements[key]
		if pstate == nil {
			pstate = &placementState{placement: placement}
			m.placements[key] = pstate
		}
		if !pstate.created {
			options := kitty.Options{
				Action: kitty.Put, ID: int(placement.ImageID), PlacementID: int(placement.PlacementID),
				Columns: placement.Columns, Rows: placement.Rows, VirtualPlacement: true, Quite: 1,
			}
			command.WriteString(wrapForEnvelope(ansi.KittyGraphics(nil, options.Options()...), m.envelope))
			pstate.created = true
		}
	}
	m.evictLocked(&command, visible)
	m.reindexTransfersLocked()
	if err := flushCommand(); err != nil {
		for _, path := range tempFiles {
			_ = os.Remove(path)
			for imageID, tracked := range m.transferFiles {
				if tracked == path {
					delete(m.transferFiles, imageID)
				}
			}
			for _, state := range m.sources {
				for imageID, tracked := range state.transfers {
					if tracked == path {
						delete(state.transfers, imageID)
					}
				}
			}
		}
		m.invalidateLocked()
		return err
	}
	m.applied = cloneFrame(m.desired)
	m.appliedCapability = m.capability
	m.dirty = false
	return nil
}

// pruneUploadsLocked keeps the per-source live image-ID set bounded. The oldest
// IDs are deleted from the terminal first; keeping them would slowly leak
// terminal-side image memory across a long session of scroll-driven evictions.
func (m *Manager) pruneUploadsLocked(command *bytes.Buffer, state *sourceState, keep uint32) {
	for len(state.uploaded) >= maxUploadsPerSource {
		var victim uint32
		for id := range state.uploaded {
			if id != keep && (victim == 0 || id < victim) {
				victim = id
			}
		}
		if victim == 0 {
			return
		}
		writeDeleteResource(command, victim, m.envelope)
		if path := state.transfers[victim]; path != "" {
			_ = os.Remove(path)
		}
		delete(state.transfers, victim)
		delete(m.transferFiles, victim)
		delete(state.uploaded, victim)
		for _, tracked := range m.placements {
			if tracked.placement.Source.Key == state.source.Key && tracked.placement.ImageID == victim {
				tracked.created = false
			}
		}
	}
}

// reindexTransfersLocked rebuilds transferFiles from the per-source records.
// The map is only an ID->path lookup for ObserveResponse; deriving it instead of
// mutating it in parallel is what stops entries surviving the source that owns
// them and leaking temp files into TMPDIR for the rest of the session.
func (m *Manager) reindexTransfersLocked() {
	live := make(map[uint32]string, len(m.transferFiles))
	for _, state := range m.sources {
		for id, path := range state.transfers {
			live[id] = path
		}
	}
	for id, path := range m.transferFiles {
		if _, ok := live[id]; !ok {
			_ = os.Remove(path)
		}
	}
	m.transferFiles = live
}

func (m *Manager) evictLocked(command *bytes.Buffer, visible map[string]bool) {
	for len(m.sources) > maxRetainedImages {
		var oldestKey string
		var oldest uint64 = ^uint64(0)
		for key, state := range m.sources {
			if visible[key] || state.lastUsed >= oldest {
				continue
			}
			oldestKey, oldest = key, state.lastUsed
		}
		if oldestKey == "" {
			return
		}
		state := m.sources[oldestKey]
		for id := range state.uploaded {
			writeDeleteResource(command, id, m.envelope)
		}
		for id, path := range state.transfers {
			_ = os.Remove(path)
			delete(m.transferFiles, id)
		}
		if path := m.transferFiles[state.imageID]; path != "" {
			_ = os.Remove(path)
			delete(m.transferFiles, state.imageID)
		}
		delete(m.sources, oldestKey)
		for key, placement := range m.placements {
			if placement.placement.Source.Key == oldestKey {
				delete(m.placements, key)
			}
		}
	}
}

func (m *Manager) Release(w io.Writer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var command bytes.Buffer
	for _, state := range m.sources {
		for id := range state.uploaded {
			writeDeleteResource(&command, id, m.envelope)
		}
	}
	for _, path := range m.probeFiles {
		_ = os.Remove(path)
	}
	for _, path := range m.transferFiles {
		_ = os.Remove(path)
	}
	for _, state := range m.sources {
		for _, path := range state.transfers {
			_ = os.Remove(path)
		}
	}
	if _, err := command.WriteTo(w); err != nil {
		return err
	}
	m.desired, m.applied = Frame{}, Frame{}
	m.sources = make(map[string]*sourceState)
	m.placements = make(map[string]*placementState)
	m.probeFiles = make(map[uint32]string)
	m.transferFiles = make(map[uint32]string)
	m.dirty = false
	return nil
}

func writeUpload(w io.Writer, source Source, imageID uint32, transport Transport, envelope Envelope) (string, error) {
	if transport == TemporaryFile {
		f, err := os.CreateTemp("", "swarm-tty-graphics-protocol-image-*")
		if err != nil {
			return "", err
		}
		path := f.Name()
		if _, err = f.Write(source.PNG); err == nil {
			err = f.Close()
		} else {
			_ = f.Close()
		}
		if err != nil {
			_ = os.Remove(path)
			return "", err
		}
		payload := base64.StdEncoding.EncodeToString([]byte(path))
		sequence := ansi.KittyGraphics([]byte(payload), "a=t", "i="+uintString(imageID),
			"f=100", "t=t", "q=1")
		if err := writeAll(w, []byte(wrapForEnvelope(sequence, envelope))); err != nil {
			_ = os.Remove(path)
			return "", err
		}
		return path, nil
	}

	// Encode directly into protocol-sized chunks. Encoding the complete image
	// first temporarily doubles the image's memory footprint (and makes the
	// render goroutine spend a long, uninterruptible interval in one large
	// allocation) for no protocol benefit. Keep raw chunk boundaries aligned
	// to 3-byte base64 quanta so every emitted payload is independently valid.
	rawChunkSize := (kitty.MaxChunkSize / 4) * 3
	if rawChunkSize <= 0 {
		rawChunkSize = 3
	}
	for offset := 0; offset < len(source.PNG); {
		end := min(offset+rawChunkSize, len(source.PNG))
		encodedLen := base64.StdEncoding.EncodedLen(end - offset)
		last := end == len(source.PNG)
		encoded := make([]byte, encodedLen)
		base64.StdEncoding.Encode(encoded, source.PNG[offset:end])
		var options []string
		if offset == 0 {
			options = []string{"a=t", "i=" + uintString(imageID), "f=100", "q=1"}
		} else {
			options = []string{"q=1"}
		}
		if offset != 0 || !last {
			if last {
				options = append(options, "m=0")
			} else {
				options = append(options, "m=1")
			}
		}
		if err := writeAll(w, []byte(wrapForEnvelope(ansi.KittyGraphics(encoded, options...), envelope))); err != nil {
			return "", err
		}
		offset = end
	}
	return "", nil
}

func writeDeleteResource(w io.Writer, imageID uint32, envelope Envelope) {
	options := kitty.Options{
		Action: kitty.Delete, Delete: kitty.DeleteID, DeleteResources: true,
		ID: int(imageID), Quite: 2,
	}
	_, _ = io.WriteString(w, wrapForEnvelope(ansi.KittyGraphics(nil, options.Options()...), envelope))
}

func sanitizeProtocolError(message string) string {
	message = stringsTrimSpace(message)
	if message == "" {
		return "terminal rejected the image"
	}
	if len(message) > 160 {
		message = message[:160]
	}
	return "terminal rejected the image: " + message
}

func placementKey(source, occurrence string, columns, rows int) string {
	return source + "\x00" + occurrence + "\x00" + strconv.Itoa(columns) + "x" + strconv.Itoa(rows)
}

func (m *Manager) allocateID() uint32 {
	for {
		id := processIDs.Add(1)
		if id != 0 {
			return id
		}
	}
}

// allocatePlacementID returns a placement identity that survives the wire
// encoding intact.
//
// Image IDs are 32 bits, but placement IDs are not: they travel to the terminal
// inside an SGR underline color (see protocol.go), which carries 24 bits. The
// mask therefore belongs at ALLOCATION, not only at encoding — masking only on
// the way out would let the manager hold an ID the terminal never sees, so a
// later delete or re-place would address a placement that does not exist.
//
// The two namespaces are independent in the Kitty protocol, so a placement ID
// numerically equal to some image ID is not a collision. What would be a
// collision is two LIVE placements of the SAME image sharing a placement ID,
// which requires 2^24 allocations between them while both remain live. IDs are
// allocated once per distinct (source, occurrence, columns, rows) and cached in
// m.placements, so that is 16,777,216 distinct image occurrences in one
// process — not reachable. TestPlacementIDSurvivesEncoding pins the invariant
// that actually matters: what the allocator hands out is what the wire carries.
func (m *Manager) allocatePlacementID() uint32 {
	for {
		id := m.allocateID() & 0x00ffffff
		if id != 0 {
			return id
		}
	}
}

func cloneFrame(frame Frame) Frame {
	frame.Placements = append([]Placement(nil), frame.Placements...)
	return frame
}

func framesEqual(a, b Frame) bool {
	if len(a.Placements) != len(b.Placements) {
		return false
	}
	for i := range a.Placements {
		x, y := a.Placements[i], b.Placements[i]
		if x.ImageID != y.ImageID || x.PlacementID != y.PlacementID ||
			x.Columns != y.Columns || x.Rows != y.Rows {
			return false
		}
	}
	return true
}

func uintString(value uint32) string { return strconv.FormatUint(uint64(value), 10) }

func cleanupStaleTempFiles() {
	pattern := filepath.Join(os.TempDir(), "swarm-tty-graphics-protocol-*")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(path)
	}
}

// Tiny local helpers keep protocol code free from fmt-heavy hot paths.
type stringsBuilder struct{ bytes.Buffer }

func (b *stringsBuilder) WriteString(s string) { _, _ = b.Buffer.WriteString(s) }
func stringsTrimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r' || s[start] == '\n') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}
