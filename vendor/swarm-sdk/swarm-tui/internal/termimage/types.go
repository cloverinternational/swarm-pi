// Package termimage emits Kitty graphics protocol resources and Unicode
// placeholders without maintaining a second terminal-coordinate system.
package termimage

// Capability reports whether native terminal images are available.
type Capability uint8

const (
	Unknown Capability = iota
	Kitty
	Unsupported
)

// Transport is the confirmed image data transfer mechanism.
type Transport uint8

const (
	Direct Transport = iota
	TemporaryFile
)

// Envelope describes how Kitty APC commands reach the terminal.
type Envelope uint8

const (
	Raw Envelope = iota
	TmuxPassthrough
)

// Source is a validated PNG resource. Key must identify the content, not its
// filename, so replacement bytes cannot accidentally reuse stale pixels.
type Source struct {
	Key           string
	PNG           []byte
	Width, Height int
}

// Placement is a virtual Kitty placement rendered by Unicode placeholder cells.
type Placement struct {
	Source               Source
	Occurrence           string
	Columns, Rows        int
	ImageID, PlacementID uint32
}

// Frame is the complete visible set of virtual placements for one text frame.
type Frame struct {
	Placements []Placement
}
