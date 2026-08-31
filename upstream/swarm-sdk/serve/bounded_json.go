// Package serve — bounded_json.go
//
// A from-scratch, package-local structural JSON encoder for every arbitrary
// outbound value this package writes (HTTP JSON-RPC responses, WebSocket
// responses/notifications, and SSE/stream envelopes). It exists because
// encoding/json's Marshal/Encoder.Encode have no pre-allocation byte bound
// and will happily invoke an arbitrary reachable json.Marshaler or
// encoding.TextMarshaler — including one that blocks, panics, or allocates
// without limit. A method handler's result (and an *ErrorObject's Data) can
// originate from arbitrary/adversarial content (LLM provider output, peer
// content, user-controlled config), so this package encodes that surface
// itself instead of trusting encoding/json with it.
//
// Design summary (see CONTRACT.md "Bounded encoding contract"):
//
//   - A single 4 MiB type-inclusive maximum bounds the encoded output,
//     including the envelope and any trailing newline the caller appends.
//   - Every append to the output buffer is preceded by a capacity check
//     against that maximum; large strings are escaped, and byte slices are
//     base64-encoded, in small fixed-size chunks so a single huge value
//     cannot allocate an unbounded intermediate buffer before the cap is
//     discovered.
//   - Nesting depth and total visited-node count are both bounded, pointers
//     and self-referential maps/slices are cycle-detected via a
//     visit-in-progress set (not just a seen-once set, so legitimate DAGs —
//     the same pointer reachable twice via different branches — still
//     encode fine), and context cancellation is polled periodically.
//   - Every reachable json.Marshaler or encoding.TextMarshaler is rejected
//     (response_encoding_error) without ever being invoked — including
//     json.RawMessage's own MarshalJSON, which is special-cased instead:
//     validated with json.Valid (a pure structural check, not a marshal
//     call) and copied bounded, never invoking arbitrary code either way.
//   - Nothing in this file ever calls an arbitrary value's Error() method.
package serve

import (
	"context"
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Bounds for the structural encoder. All three are package variables (not
// constants) so tests can shrink them to exercise cap/depth/node/cycle
// behavior deterministically without allocating megabyte-sized fixtures for
// every case.
var (
	// maxBoundedJSONBytes is the exact type-inclusive maximum for one
	// encoded value, matching the transport-wide arbitrary-response cap
	// from CONTRACT.md. It includes everything the encoder itself writes
	// (the full envelope); a caller-appended trailing newline is counted
	// against it too by callers that add one after encoding.
	maxBoundedJSONBytes = 4 << 20 // 4 MiB

	// maxBoundedJSONDepth bounds structural nesting (objects/arrays and the
	// pointer/interface indirections between them). It exists independently
	// of the byte cap because a deeply nested-but-narrow value (or a long
	// pointer chain) can be tiny in bytes yet blow a real (non-tail-call)
	// recursive walker's goroutine stack.
	maxBoundedJSONDepth = 32

	// maxBoundedJSONNodes bounds the total number of values visited across
	// the whole encode, independent of the byte cap: many small nodes (for
	// example millions of one-byte numbers in a deeply nested but
	// low-fanout structure) can still be expensive to walk even when the
	// resulting bytes would fit.
	maxBoundedJSONNodes = 500000
)

// Stable, machine-checkable Error.Data categories used by callers that build
// a fallback JSON-RPC error Response when bounded encoding fails. Message
// text matches CONTRACT.md exactly for the oversize case.
const (
	responseTooLargeData      = "response_too_large"
	responseEncodingErrorData = "response_encoding_error"

	responseTooLargeMessage = "response exceeds maximum size"
	responseEncodingMessage = "response could not be encoded"
)

// boundedEncodeErrorKind distinguishes "would not fit" from "cannot be
// safely encoded at all" so callers can choose the correct stable fallback
// category without inspecting error text.
type boundedEncodeErrorKind int

const (
	boundedEncodeOversize boundedEncodeErrorKind = iota
	boundedEncodeInvalid
)

// boundedEncodeError is the only error type encodeBoundedJSON returns for an
// encoding-shaped failure (as opposed to context cancellation, which is
// returned verbatim as ctx.Err()).
type boundedEncodeError struct {
	kind boundedEncodeErrorKind
}

func (e *boundedEncodeError) Error() string {
	if e.kind == boundedEncodeOversize {
		return "bounded json: value exceeds maximum size"
	}
	return "bounded json: value cannot be safely encoded"
}

var (
	errBoundedOversize error = &boundedEncodeError{kind: boundedEncodeOversize}
	errBoundedInvalid  error = &boundedEncodeError{kind: boundedEncodeInvalid}
)

// Exported, errors.Is-compatible sentinels for the two ways a bounded
// encode can fail (see boundedEncodeErrorKind above). A caller outside this
// package — for example Worker 2's /api/peers handler, which consumes
// EncodeBoundedJSON directly — can branch on these with a plain
// `errors.Is(err, serve.ErrResponseTooLarge)` / `errors.Is(err,
// serve.ErrResponseEncoding)` without needing access to the unexported
// *boundedEncodeError type; *boundedEncodeError.Unwrap (below) is what
// makes that match succeed. Context cancellation is returned verbatim as
// ctx.Err() by every encode entry point in this file and is deliberately
// never wrapped by either sentinel, so a caller can still distinguish
// cancellation with its own `errors.Is(err, context.Canceled)` /
// `errors.Is(err, context.DeadlineExceeded)` check.
var (
	ErrResponseTooLarge = errors.New("serve: response exceeds maximum size")
	ErrResponseEncoding = errors.New("serve: response could not be encoded")
)

// Unwrap makes *boundedEncodeError transparent to errors.Is against the
// exported ErrResponseTooLarge/ErrResponseEncoding sentinels above, per
// error kind.
func (e *boundedEncodeError) Unwrap() error {
	if e.kind == boundedEncodeOversize {
		return ErrResponseTooLarge
	}
	return ErrResponseEncoding
}

// classifyBoundedError maps any error returned by encodeBoundedJSON to a
// fallback kind. Anything that is not a *boundedEncodeError (context
// cancellation, or — defensively — any future error type) is treated as
// "invalid" rather than "oversize": oversize is the narrower, more specific
// claim and must only be made when the encoder itself detected a genuine
// capacity overrun.
func classifyBoundedError(err error) boundedEncodeErrorKind {
	if be, ok := err.(*boundedEncodeError); ok {
		return be.kind
	}
	return boundedEncodeInvalid
}

var (
	jsonMarshalerType  = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType  = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	jsonRawMessageType = reflect.TypeOf(json.RawMessage(nil))

	// timeType lets the encoder special-case time.Time (see encodeTime)
	// instead of rejecting it via typeRejected below. time.Time implements
	// both json.Marshaler and encoding.TextMarshaler, so without this
	// special case EVERY struct with a time.Time field anywhere in its
	// reachable graph — which is nearly every response type in this
	// codebase (ConversationSummary.UpdatedAt, client.State.UpdatedAt,
	// etc.) — would be unconditionally rejected as response_encoding_error,
	// breaking the method entirely rather than protecting against anything
	// adversarial. time.Time's MarshalJSON is a fixed-format, bounded,
	// standard-library operation (RFC3339Nano quoted string, well under 64
	// bytes) — safe to special-case the same way json.RawMessage already
	// is above.
	timeType = reflect.TypeOf(time.Time{})
)

// typeRejected reports whether values of type t must never be handed to the
// encoder's normal structural path: t (or *t, matching how most standard
// library and third-party types implement these interfaces with a pointer
// receiver) implements json.Marshaler or encoding.TextMarshaler. Checking
// is purely type-level reflection — it never constructs or calls a value of
// t, so a type with an expensive, blocking, or panicking MarshalJSON/
// MarshalText method is rejected without ever running that method.
func typeRejected(t reflect.Type) bool {
	if t.Implements(jsonMarshalerType) || t.Implements(textMarshalerType) {
		return true
	}
	pt := reflect.PointerTo(t)
	return pt.Implements(jsonMarshalerType) || pt.Implements(textMarshalerType)
}

// boundedEncodeState carries the bounded output buffer plus the walker's
// depth/node/cycle-detection bookkeeping for one top-level encodeBoundedJSON
// call. It is not safe for concurrent use; callers construct a fresh one per
// encode.
type boundedEncodeState struct {
	buf    []byte
	max    int
	nodes  int
	ctx    context.Context
	active map[uintptr]bool
}

// checkBudget enforces the node-count and context-cancellation bounds that
// apply to every visited value regardless of its kind. It is called once per
// encode() invocation, before any type-specific work.
func (s *boundedEncodeState) checkBudget() error {
	s.nodes++
	if s.nodes > maxBoundedJSONNodes {
		return errBoundedInvalid
	}
	// Poll cancellation every 256 nodes rather than every node: a context
	// Done() channel receive is cheap but not free, and the byte/node caps
	// above already bound total work to a small, fixed amount even without
	// polling every single node.
	if s.nodes&0xFF == 0 {
		if err := s.ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

// grow is the single choke point every byte this encoder ever writes passes
// through. The capacity check against s.max happens before the append that
// would grow the buffer, so a value that would exceed the cap is rejected
// before Go allocates the larger backing array for it — not after.
func (s *boundedEncodeState) grow(p []byte) error {
	if len(s.buf)+len(p) > s.max {
		return errBoundedOversize
	}
	s.buf = append(s.buf, p...)
	return nil
}

func (s *boundedEncodeState) writeByte(c byte) error {
	if len(s.buf)+1 > s.max {
		return errBoundedOversize
	}
	s.buf = append(s.buf, c)
	return nil
}

func (s *boundedEncodeState) writeString(str string) error {
	if len(s.buf)+len(str) > s.max {
		return errBoundedOversize
	}
	s.buf = append(s.buf, str...)
	return nil
}

// jsonHexDigits is used by encodeString's \u00XX control-character escapes.
const jsonHexDigits = "0123456789abcdef"

// encodeString writes str as a JSON string literal, escaping it in small
// fixed-size chunks so that even a single enormous or heavily-escaping
// (every byte expands to \u00XX, a 6x blowup) string is bounds-checked
// incrementally instead of first building one huge escaped copy and
// checking its size only afterward.
func (s *boundedEncodeState) encodeString(str string) error {
	if err := s.writeByte('"'); err != nil {
		return err
	}
	const chunkCap = 4096
	var chunk [chunkCap]byte
	n := 0
	flush := func() error {
		if n == 0 {
			return nil
		}
		err := s.grow(chunk[:n])
		n = 0
		return err
	}
	var one [utf8.UTFMax]byte
	for i := 0; i < len(str); {
		r, size := utf8.DecodeRuneInString(str[i:])
		i += size
		var esc []byte
		switch r {
		case '"':
			esc = []byte(`\"`)
		case '\\':
			esc = []byte(`\\`)
		case '\n':
			esc = []byte(`\n`)
		case '\r':
			esc = []byte(`\r`)
		case '\t':
			esc = []byte(`\t`)
		default:
			switch {
			case r < 0x20:
				esc = []byte{'\\', 'u', '0', '0', jsonHexDigits[(r>>4)&0xF], jsonHexDigits[r&0xF]}
			case r == utf8.RuneError && size == 1:
				esc = []byte(`\ufffd`)
			default:
				rl := utf8.EncodeRune(one[:], r)
				esc = one[:rl]
			}
		}
		if n+len(esc) > chunkCap {
			if err := flush(); err != nil {
				return err
			}
		}
		n += copy(chunk[n:], esc)
	}
	if err := flush(); err != nil {
		return err
	}
	return s.writeByte('"')
}

// encodeBytesBase64 writes b as a base64 (standard encoding) JSON string
// literal, one bounded chunk at a time. Each chunk's destination buffer is
// sized and capacity-checked against the overall cap *before* it is
// allocated, so a single call never allocates an unbounded encode buffer for
// an attacker-supplied byte slice before discovering the value is oversize.
func (s *boundedEncodeState) encodeBytesBase64(b []byte) error {
	if err := s.writeByte('"'); err != nil {
		return err
	}
	const chunkIn = 3 * 4096 // multiple of 3 so every chunk's base64 output is unpadded except possibly the last
	enc := base64.StdEncoding
	for i := 0; i < len(b); i += chunkIn {
		end := i + chunkIn
		if end > len(b) {
			end = len(b)
		}
		in := b[i:end]
		outLen := enc.EncodedLen(len(in))
		if len(s.buf)+outLen > s.max {
			return errBoundedOversize
		}
		dst := make([]byte, outLen)
		enc.Encode(dst, in)
		s.buf = append(s.buf, dst...)
	}
	return s.writeByte('"')
}

// encodeRawMessage validates raw as syntactically well-formed JSON using
// json.Valid — a pure structural scan of the standard library's own parser
// that never invokes a Marshaler/TextMarshaler on anything — and then copies
// it bounded. json.RawMessage technically implements json.Marshaler itself
// (it returns its own bytes), which is exactly why callers must route it
// through this function instead of the generic typeRejected path: it is
// validated and copied directly, never via a MarshalJSON call.
func (s *boundedEncodeState) encodeRawMessage(raw json.RawMessage) error {
	if len(raw) == 0 {
		return s.writeString("null")
	}
	if len(s.buf)+len(raw) > s.max {
		return errBoundedOversize
	}
	if !json.Valid(raw) {
		return errBoundedInvalid
	}
	return s.grow(raw)
}

// encodeTime writes t as an RFC3339Nano-formatted JSON string, matching
// time.Time.MarshalJSON's own output format exactly. It formats the value
// itself with time.Time.AppendFormat rather than calling t.MarshalJSON(),
// so this file's rule "never invoke a reachable Marshaler" holds even for
// this one deliberate exemption — AppendFormat is not part of the
// json.Marshaler/encoding.TextMarshaler contract this package otherwise
// refuses to invoke.
func (s *boundedEncodeState) encodeTime(t time.Time) error {
	var buf [64]byte
	out := t.AppendFormat(buf[:0], time.RFC3339Nano)
	return s.encodeString(string(out))
}

// isEmptyValue mirrors encoding/json's definition of "empty" for the
// purposes of an omitempty struct tag.
func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	}
	return false
}

// structFieldName resolves the JSON field name and omitempty-ness for a
// struct field from its `json:"..."` tag, matching encoding/json's own
// simple (non-embedding) tag grammar: "-" skips the field entirely, an empty
// tag falls back to the Go field name, and ",omitempty" after the name is
// honored.
func structFieldName(f reflect.StructField) (name string, omitempty, skip bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}
	name = f.Name
	if tag == "" {
		return name, false, false
	}
	parts := strings.Split(tag, ",")
	if parts[0] != "" {
		name = parts[0]
	}
	for _, p := range parts[1:] {
		if p == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty, false
}

// encode is the recursive structural walker. It supports ordinary JSON
// primitives, pointers/interfaces (dereferenced/unwrapped with cycle
// detection), arrays/slices (byte slices become base64 strings), string-
// keyed maps (key order sorted for determinism), structs with JSON tags,
// and validated json.RawMessage. Every other kind (chan, func, complex,
// unsafe pointer, non-string-keyed map, NaN/Inf float) — and every type
// that implements json.Marshaler or encoding.TextMarshaler — is rejected as
// unsupported rather than approximated.
func (s *boundedEncodeState) encode(v reflect.Value, depth int) error {
	if err := s.checkBudget(); err != nil {
		return err
	}
	if depth > maxBoundedJSONDepth {
		return errBoundedInvalid
	}
	if !v.IsValid() {
		return s.writeString("null")
	}

	// Pointers and interfaces are unwrapped first, before any type-level
	// check, so the check below always runs against the concrete resolved
	// type — otherwise a Marshaler-implementing concrete value stored in an
	// `any` field would slip past a type check performed against the empty
	// interface type itself.
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return s.writeString("null")
		}
		return s.encode(v.Elem(), depth+1)
	case reflect.Ptr:
		if v.IsNil() {
			return s.writeString("null")
		}
		ptr := v.Pointer()
		if s.active[ptr] {
			return errBoundedInvalid // cycle
		}
		s.active[ptr] = true
		defer delete(s.active, ptr)
		return s.encode(v.Elem(), depth+1)
	}

	if v.Type() == jsonRawMessageType {
		return s.encodeRawMessage(v.Interface().(json.RawMessage))
	}
	if v.Type() == timeType {
		return s.encodeTime(v.Interface().(time.Time))
	}
	if typeRejected(v.Type()) {
		return errBoundedInvalid
	}

	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			return s.writeString("true")
		}
		return s.writeString("false")
	case reflect.String:
		return s.encodeString(v.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return s.grow(strconv.AppendInt(nil, v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return s.grow(strconv.AppendUint(nil, v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return errBoundedInvalid
		}
		bits := 64
		if v.Kind() == reflect.Float32 {
			bits = 32
		}
		return s.grow(strconv.AppendFloat(nil, f, 'g', -1, bits))
	case reflect.Slice:
		return s.encodeSlice(v, depth)
	case reflect.Array:
		return s.encodeArray(v, depth)
	case reflect.Map:
		return s.encodeMap(v, depth)
	case reflect.Struct:
		return s.encodeStruct(v, depth)
	default:
		// chan, func, complex64/128, unsafe pointer, and anything else
		// reflect can name but this encoder deliberately does not support.
		return errBoundedInvalid
	}
}

func (s *boundedEncodeState) encodeSlice(v reflect.Value, depth int) error {
	if v.IsNil() {
		return s.writeString("null")
	}
	if v.Type().Elem().Kind() == reflect.Uint8 {
		return s.encodeBytesBase64(v.Bytes())
	}
	ptr := v.Pointer()
	if s.active[ptr] {
		return errBoundedInvalid // cycle
	}
	s.active[ptr] = true
	defer delete(s.active, ptr)

	if err := s.writeByte('['); err != nil {
		return err
	}
	for i := 0; i < v.Len(); i++ {
		if i > 0 {
			if err := s.writeByte(','); err != nil {
				return err
			}
		}
		if err := s.encode(v.Index(i), depth+1); err != nil {
			return err
		}
	}
	return s.writeByte(']')
}

func (s *boundedEncodeState) encodeArray(v reflect.Value, depth int) error {
	if err := s.writeByte('['); err != nil {
		return err
	}
	for i := 0; i < v.Len(); i++ {
		if i > 0 {
			if err := s.writeByte(','); err != nil {
				return err
			}
		}
		if err := s.encode(v.Index(i), depth+1); err != nil {
			return err
		}
	}
	return s.writeByte(']')
}

// minJSONMapEntryBytes is a conservative, never-overcounting lower bound on
// the number of encoded bytes any single string-keyed map entry can ever
// contribute: an empty string key's two quote bytes, one colon, and the
// smallest possible JSON value (a one-digit number — smaller than `null`,
// `true`/`false`, `""`, `[]`, or `{}`). It deliberately omits the separating
// comma between entries, so it can only ever *undercount* true output size
// — a preflight lower bound must never reject a cardinality that could
// actually have fit.
const minJSONMapEntryBytes = 2 + 1 + 1 // `"` + `"` + `:` + one value byte

// mapCardinalityPreflight rejects an attacker-controlled map's cardinality
// before this encoder does anything whose cost is proportional to it:
// reflect.Value.MapKeys (never called anywhere in this file — see the
// package doc comment), cycle-detection bookkeeping (the s.active
// insertion), or allocating an ordering/entry slice sized by n. n =
// v.Len(), which reflect reports directly off the map header in O(1)
// without visiting a single entry, so calling this check costs nothing
// proportional to the map's size either.
//
// Two independent budgets are enforced, both overflow-safely (no
// n*constant multiplication that could wrap for an adversarial huge n on a
// 32-bit int):
//
//   - remaining node budget: every entry consumes at least one node via its
//     nested encode() call for the value, so a cardinality alone exceeding
//     the remaining node budget can never finish regardless of what the
//     values are.
//   - remaining byte budget: minJSONMapEntryBytes per entry is a true lower
//     bound on output size, so a cardinality whose minimum possible output
//     already exceeds the remaining byte budget can never fit either.
func (s *boundedEncodeState) mapCardinalityPreflight(n int) error {
	if n < 0 {
		return errBoundedInvalid
	}

	remainingNodes := maxBoundedJSONNodes - s.nodes
	if remainingNodes < 0 || n > remainingNodes {
		return errBoundedInvalid
	}

	remainingBytes := s.max - len(s.buf) - 2 // '{' and '}'
	if remainingBytes < 0 {
		return errBoundedOversize
	}
	if n > remainingBytes/minJSONMapEntryBytes {
		return errBoundedOversize
	}
	return nil
}

// boundedMapEntry is one already-resolved (key, value) pair collected by
// encodeMap's single bounded MapRange pass, ahead of the deterministic sort
// by key.
type boundedMapEntry struct {
	name string
	val  reflect.Value
}

func (s *boundedEncodeState) encodeMap(v reflect.Value, depth int) error {
	if v.Type().Key().Kind() != reflect.String {
		return errBoundedInvalid // only string-keyed maps are supported
	}
	if v.IsNil() {
		return s.writeString("null")
	}

	// Cardinality preflight runs first — before cycle bookkeeping, before
	// any ordering/entry allocation, and using neither MapKeys nor
	// MapRange — so an attacker-controlled map is rejected without any
	// allocation proportional to its size.
	n := v.Len()
	if err := s.mapCardinalityPreflight(n); err != nil {
		return err
	}

	ptr := v.Pointer()
	if s.active[ptr] {
		return errBoundedInvalid // cycle
	}
	s.active[ptr] = true
	defer delete(s.active, ptr)

	// One entry slice, sized exactly by the now-validated cardinality n,
	// replaces the previous MapKeys()+byName-map+names-slice trio: MapRange
	// visits every entry exactly once and Go map keys are already unique,
	// so no by-name deduplication step is needed to get deterministic
	// output.
	entries := make([]boundedMapEntry, 0, n)
	for iter := v.MapRange(); iter.Next(); {
		entries = append(entries, boundedMapEntry{name: iter.Key().String(), val: iter.Value()})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	if err := s.writeByte('{'); err != nil {
		return err
	}
	for i, e := range entries {
		if i > 0 {
			if err := s.writeByte(','); err != nil {
				return err
			}
		}
		if err := s.encodeString(e.name); err != nil {
			return err
		}
		if err := s.writeByte(':'); err != nil {
			return err
		}
		if err := s.encode(e.val, depth+1); err != nil {
			return err
		}
	}
	return s.writeByte('}')
}

func (s *boundedEncodeState) encodeStruct(v reflect.Value, depth int) error {
	t := v.Type()
	if err := s.writeByte('{'); err != nil {
		return err
	}
	wrote := false
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue // unexported
		}
		name, omitempty, skip := structFieldName(field)
		if skip {
			continue
		}
		fv := v.Field(i)
		if omitempty && isEmptyValue(fv) {
			continue
		}
		if wrote {
			if err := s.writeByte(','); err != nil {
				return err
			}
		}
		wrote = true
		if err := s.encodeString(name); err != nil {
			return err
		}
		if err := s.writeByte(':'); err != nil {
			return err
		}
		if err := s.encode(fv, depth+1); err != nil {
			return err
		}
	}
	return s.writeByte('}')
}

// encodeBoundedJSON encodes v under the package's full 4 MiB structural
// bound (maxBoundedJSONBytes). It never calls json.Marshal,
// (*json.Encoder).Encode, a reachable json.Marshaler or
// encoding.TextMarshaler, or Error() on any value — see the package doc
// comment above for the full contract.
//
// On success it returns the encoded bytes. On failure it returns either
// ctx.Err() (cancellation observed mid-encode) or a *boundedEncodeError
// classifying the failure as oversize or otherwise unsupported/invalid;
// classifyBoundedError normalizes either case for a caller building a
// fallback response, and *boundedEncodeError.Unwrap makes it match
// errors.Is against the exported ErrResponseTooLarge/ErrResponseEncoding
// sentinels.
func encodeBoundedJSON(ctx context.Context, v any) ([]byte, error) {
	return encodeBoundedJSONCapped(ctx, v, maxBoundedJSONBytes)
}

// EncodeBoundedJSON is the exported form of this package's bounded
// structural JSON encoder, for callers outside this package — for example
// the standalone gateway's /api/peers handler — that must bounded-encode an
// arbitrary value under the exact same structural cap this package's own
// HTTP/WebSocket/SSE/stream transports use, completely, before writing any
// response byte. It returns compact JSON with no trailing newline, at the
// full maxBoundedJSONBytes cap: WebSocket/SSE/stream no-newline payloads
// retain that full cap, while this package's own HTTP JSON-RPC transport
// separately reserves one byte for its trailing newline (see http.go's
// httpBoundedResponseEnvelope) — a caller of this exported function that
// itself appends framing bytes after the call is responsible for its own
// equivalent reservation.
//
// Failures are classified via errors.Is against ErrResponseTooLarge
// (encoded output would exceed the cap) and ErrResponseEncoding (anything
// else unsupported/cyclic/invalid). Context cancellation is returned
// verbatim as ctx.Err() and is not wrapped by either sentinel.
func EncodeBoundedJSON(ctx context.Context, v any) ([]byte, error) {
	return encodeBoundedJSONCapped(ctx, v, maxBoundedJSONBytes)
}

// encodeBoundedJSONCapped is encodeBoundedJSON/EncodeBoundedJSON
// parameterized by an explicit byte cap instead of the package-wide
// maxBoundedJSONBytes var, so a caller that must reserve trailing bytes for
// its own framing (http.go's `\n`) can bound the structural encode itself
// to leave room for that framing up front, instead of encoding at the full
// cap and then risking cap+1 once the framing byte is appended afterward.
func encodeBoundedJSONCapped(ctx context.Context, v any, max int) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if max < 0 {
		max = 0
	}
	st := &boundedEncodeState{max: max, ctx: ctx, active: make(map[uintptr]bool)}
	if err := st.encode(reflect.ValueOf(v), 0); err != nil {
		return nil, err
	}
	return st.buf, nil
}

// boundedEncodeResponse bounded-encodes a JSON-RPC Response. On success it
// returns the encoded bytes. On failure — oversize, an unsupported/cyclic
// value anywhere in Result or Error.Data, or context cancellation mid-encode
// — it builds and encodes the stable fixed fallback error Response instead:
// response_too_large (message "response exceeds maximum size") for a
// genuine capacity overrun, response_encoding_error for everything else. If
// even the fallback with the original id does not fit, the id is dropped to
// null (CONTRACT.md: "if the request ID cannot fit, use null") and encoding
// is retried once more; a hand-written constant literal is the last-resort
// fallback so this function always returns well-formed, non-empty JSON.
func boundedEncodeResponse(ctx context.Context, resp Response) []byte {
	return boundedEncodeResponseCapped(ctx, resp, maxBoundedJSONBytes)
}

// boundedEncodeResponseCapped is boundedEncodeResponse parameterized by an
// explicit byte cap. http.go's internal explicit-limit HTTP JSON-RPC path
// (httpBoundedResponseEnvelope) calls this with maxBoundedJSONBytes-1 so
// the primary encode, every fallback attempt, and the final null-id
// fallback all honor the same reduced cap that leaves exactly one byte of
// headroom for the caller's trailing newline — guaranteeing every
// normal/fallback HTTP payload, framing included, is at most
// maxBoundedJSONBytes end to end.
func boundedEncodeResponseCapped(ctx context.Context, resp Response, max int) []byte {
	if b, err := encodeBoundedJSONCapped(ctx, resp, max); err == nil {
		return b
	} else {
		fb := Response{Jsonrpc: jsonrpcVersion, ID: resp.ID, Error: fallbackEncodeError(classifyBoundedError(err))}
		if b, ferr := encodeBoundedJSONCapped(ctx, fb, max); ferr == nil {
			return b
		}
		fb.ID = nil
		if b, ferr := encodeBoundedJSONCapped(ctx, fb, max); ferr == nil {
			return b
		}
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"response could not be encoded","data":"response_encoding_error"}}`)
	}
}

// fallbackEncodeError builds the stable *ErrorObject a bounded-encode
// failure maps to, per kind.
func fallbackEncodeError(kind boundedEncodeErrorKind) *ErrorObject {
	if kind == boundedEncodeOversize {
		return NewError(ErrInternalError, responseTooLargeMessage, responseTooLargeData)
	}
	return NewError(ErrInternalError, responseEncodingMessage, responseEncodingErrorData)
}
