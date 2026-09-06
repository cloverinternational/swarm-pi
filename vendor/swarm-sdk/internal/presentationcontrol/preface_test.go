package presentationcontrol

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// TestPrefaceLittleEndianDisambiguation is a direct, numeric proof of the
// disambiguation argument documented on Preface: reading Preface's 4 bytes
// as a little-endian uint32 (matching protocol.DecodeHeader's own
// interpretation of a legacy frame's first 4 bytes) produces a value far
// outside protocol.MaxPayloadSize (16*1024*1024 = 16,777,216).
func TestPrefaceLittleEndianDisambiguation(t *testing.T) {
	const legacyMaxPayloadSize = 16 * 1024 * 1024 // protocol.MaxPayloadSize, duplicated here read-only for the assertion (this package must not import the protocol package).

	asUint32 := binary.LittleEndian.Uint32(Preface[:])
	if want := uint32(0x31435250); asUint32 != want {
		t.Fatalf("Preface as little-endian uint32 = %#x, want %#x", asUint32, want)
	}
	if asUint32 <= legacyMaxPayloadSize {
		t.Fatalf("Preface as little-endian uint32 (%d) must exceed protocol.MaxPayloadSize (%d) to be unreachable as a legacy frame length", asUint32, legacyMaxPayloadSize)
	}
	ratio := float64(asUint32) / float64(legacyMaxPayloadSize)
	if ratio < 40 {
		t.Fatalf("Preface/MaxPayloadSize ratio = %.2f, want a large margin (>= 40x, actual design ~49.3x)", ratio)
	}
}

// TestReadPrefaceMatched proves that a connection which sends Preface
// followed by a JSON envelope is detected as matched, and that the
// envelope bytes remain fully readable (with the preface itself stripped)
// from the returned wrapped conn.
func TestReadPrefaceMatched(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	envelopeBytes := []byte(`{"schema_version":"1.0","plane":"presentation","op":"presentation.ping","correlation_id":"abc"}` + "\n")

	writeDone := make(chan error, 1)
	go func() {
		if _, err := clientConn.Write(Preface[:]); err != nil {
			writeDone <- err
			return
		}
		_, err := clientConn.Write(envelopeBytes)
		writeDone <- err
	}()

	matched, wrapped, err := ReadPreface(serverConn)
	if err != nil {
		t.Fatalf("ReadPreface returned error: %v", err)
	}
	if !matched {
		t.Fatalf("matched = false, want true")
	}

	got := make([]byte, len(envelopeBytes))
	if _, err := io.ReadFull(wrapped, got); err != nil {
		t.Fatalf("reading envelope bytes from wrapped failed: %v", err)
	}
	if !bytes.Equal(got, envelopeBytes) {
		t.Fatalf("bytes read from wrapped = %q, want %q (preface must be stripped, envelope bytes intact)", got, envelopeBytes)
	}

	select {
	case werr := <-writeDone:
		if werr != nil {
			t.Fatalf("client write failed: %v", werr)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for client write goroutine")
	}
}

// TestReadPrefaceNotMatched is the critical byte-preservation test: a
// connection sends 4 arbitrary NON-matching bytes followed by more data,
// and ReadPreface must report matched=false while leaving ALL bytes
// (including the 4 peeked ones) readable from wrapped, in the original
// order, byte for byte -- proving the peek never destructively consumed
// data the legacy dispatch path needs.
func TestReadPrefaceNotMatched(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	// 4 arbitrary bytes that do NOT match Preface ('P','R','C','1'),
	// followed by a chunk of additional legacy-looking payload bytes.
	nonMatching := []byte{0x01, 0x02, 0x03, 0x04}
	trailing := []byte("this is definitely legacy-protocol payload data, not JSON")
	full := append(append([]byte{}, nonMatching...), trailing...)

	writeDone := make(chan error, 1)
	go func() {
		_, err := clientConn.Write(full)
		writeDone <- err
	}()

	matched, wrapped, err := ReadPreface(serverConn)
	if err != nil {
		t.Fatalf("ReadPreface returned error: %v", err)
	}
	if matched {
		t.Fatalf("matched = true, want false for non-matching preface bytes %v", nonMatching)
	}

	got := make([]byte, len(full))
	if _, err := io.ReadFull(wrapped, got); err != nil {
		t.Fatalf("reading all bytes from wrapped failed: %v", err)
	}
	if !bytes.Equal(got, full) {
		t.Fatalf("bytes read from wrapped = %q, want %q (exact original order, including the 4 peeked bytes)", got, full)
	}

	select {
	case werr := <-writeDone:
		if werr != nil {
			t.Fatalf("client write failed: %v", werr)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for client write goroutine")
	}
}

// TestReadPrefaceWrappedPreservesOtherConnMethods proves the wrapped conn
// returned by ReadPreface still promotes non-Read net.Conn methods (Write,
// Close, deadlines) through to the underlying conn, since a caller
// (dispatcher or legacy path) needs full net.Conn behavior, not just Read.
func TestReadPrefaceWrappedPreservesOtherConnMethods(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	go func() {
		_, _ = clientConn.Write(Preface[:])
	}()

	_, wrapped, err := ReadPreface(serverConn)
	if err != nil {
		t.Fatalf("ReadPreface returned error: %v", err)
	}

	if wrapped.LocalAddr() == nil {
		t.Fatalf("wrapped.LocalAddr() = nil, want the underlying conn's address")
	}
	if err := wrapped.SetDeadline(time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("wrapped.SetDeadline failed: %v", err)
	}
	if err := wrapped.Close(); err != nil {
		t.Fatalf("wrapped.Close() failed: %v", err)
	}
}
