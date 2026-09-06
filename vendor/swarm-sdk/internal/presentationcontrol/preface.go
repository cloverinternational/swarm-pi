package presentationcontrol

import (
	"bufio"
	"bytes"
	"net"
)

// Preface is the fixed 4-byte ASCII sequence a connection sends before any
// presentation-plane traffic, chosen to be unambiguous against the legacy
// binary automation protocol
// (swarm-tui/internal/headless/automation/protocol.Message's
// [4-byte little-endian length][1-byte type][payload] framing,
// protocol.HeaderSize=5).
//
// Disambiguation argument (exact numeric comparison, per this package's
// CONTRACT.md "Shared type seam"):
//
// protocol.DecodeHeader interprets a frame's first 4 bytes as a
// little-endian uint32 length via binary.LittleEndian.Uint32(data[0:4]),
// then rejects any length greater than protocol.MaxPayloadSize
// (16*1024*1024 = 16,777,216 bytes). Preface's bytes are
// {0x50, 0x52, 0x43, 0x31} ('P','R','C','1'). Read as that same
// little-endian uint32, Preface decodes to:
//
//	0x31435250 = 826,495,568
//
// which is roughly 49.3x larger than protocol.MaxPayloadSize
// (826,495,568 / 16,777,216 ≈ 49.27). protocol.Decode/DecodeHeader would
// therefore reject any legacy frame whose declared length equals Preface's
// bytes with "message too large" before ever reading a type byte or
// payload -- no legitimate legacy client can produce this length, and any
// connection that leads with these exact 4 bytes is unambiguously either a
// presentation-plane preface or a malformed/hostile legacy frame that the
// legacy decoder would have rejected anyway. This makes Preface safe to
// peek-and-branch on ahead of the legacy dispatch path without any risk of
// misinterpreting a real legacy frame.
var Preface = [4]byte{'P', 'R', 'C', '1'}

// prefaceConn adapts a bufio.Reader wrapping a net.Conn back into a
// net.Conn: every method except Read is promoted directly from the
// embedded net.Conn (Write, Close, deadlines, addresses, ...); Read is
// overridden to read from the bufio.Reader instead of the raw conn so that
// any bytes already buffered by a prior Peek are not lost.
type prefaceConn struct {
	net.Conn
	r *bufio.Reader
}

// Read implements net.Conn by delegating to the bufio.Reader, which
// transparently returns any previously peeked-but-unconsumed bytes before
// resuming reads from the underlying connection.
func (c *prefaceConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

// ReadPreface peeks at the first 4 bytes available on conn without
// destructively consuming them, using a bufio.Reader-based peek-and-wrap
// mechanism (net.Conn itself has no native unread-byte/pushback support):
// it wraps conn in a bufio.Reader and calls Peek(4), which buffers those
// bytes internally without advancing past them. It then compares the
// peeked bytes against Preface.
//
// In BOTH the matched and unmatched cases, ReadPreface returns a wrapped
// net.Conn backed by the same bufio.Reader used for the peek, so no bytes
// are ever lost regardless of outcome -- because Peek never consumes on
// its own, this holds whether or not the preface matched:
//
//   - matched == false: the peeked bytes are left buffered and unconsumed,
//     so wrapped's first Read(s) replay them verbatim, in original order,
//     followed by the rest of the underlying conn's stream. A caller can
//     hand wrapped directly to the LEGACY dispatch path with zero bytes
//     lost.
//   - matched == true: ReadPreface itself Discards exactly the 4 buffered
//     preface bytes (an in-buffer pointer advance only -- no additional
//     network I/O, so it cannot block or drop data) before returning, so
//     wrapped's first Read starts immediately at the presentation-plane
//     envelope traffic that follows the preface on the wire, with the
//     preface itself already stripped.
//
// If conn yields fewer than 4 bytes before closing or erroring, matched is
// false, wrapped still replays whatever bytes were available, and err
// reports the underlying short-read/EOF condition.
func ReadPreface(conn net.Conn) (matched bool, wrapped net.Conn, err error) {
	r := bufio.NewReaderSize(conn, 4096)
	wrapped = &prefaceConn{Conn: conn, r: r}

	peeked, peekErr := r.Peek(len(Preface))
	matched = len(peeked) == len(Preface) && bytes.Equal(peeked, Preface[:])
	if peekErr != nil {
		return false, wrapped, peekErr
	}
	if matched {
		// The preface itself is not presentation-plane payload: consume
		// (Discard) exactly the buffered preface bytes so that wrapped's
		// subsequent Read calls hand the caller the envelope traffic that
		// follows the preface, not the preface bytes themselves. Discard
		// only advances the bufio.Reader's internal buffer pointer over
		// bytes already buffered by Peek above; it performs no additional
		// network I/O and cannot block or lose data.
		if _, discardErr := r.Discard(len(Preface)); discardErr != nil {
			return true, wrapped, discardErr
		}
		return true, wrapped, nil
	}
	return matched, wrapped, nil
}
