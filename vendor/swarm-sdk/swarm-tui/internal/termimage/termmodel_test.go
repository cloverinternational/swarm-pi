package termimage

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// termModel is a deliberately small model of the terminal side of the Kitty
// graphics protocol. It parses the exact byte stream Manager writes (optionally
// unwrapping tmux DCS passthrough) and keeps only the state that decides
// whether a virtual placement can actually paint pixels: which image IDs are
// currently transmitted, which (imageID, placementID) virtual placements
// currently exist, and which temp-file transfers were referenced. Anything the
// manager believes is "applied" but the model says is absent is exactly the
// user-visible "blank space where the image should be" failure.
type termModel struct {
	images     map[uint32]bool   // transmitted and not since deleted
	placements map[uint64]bool   // (imageID<<32|placementID) created, not deleted
	tempPaths  map[uint32]string // last t=t path referenced per image ID
	// inFlight tracks a chunked direct transmission so continuation chunks
	// (which carry no i= key) are attributed to the right image.
	inFlight  uint32
	commands  []apcCommand
	bytesSeen int
}

type apcCommand struct {
	keys    map[string]string
	payload string
	raw     string
}

func newTermModel() *termModel {
	return &termModel{
		images:     map[uint32]bool{},
		placements: map[uint64]bool{},
		tempPaths:  map[uint32]string{},
	}
}

func placementHandle(imageID, placementID uint32) uint64 {
	return uint64(imageID)<<32 | uint64(placementID)
}

// unwrapTmux splits a stream into the concatenated contents of every tmux DCS
// passthrough envelope (with doubled ESC bytes restored) and everything that
// was outside an envelope. tmux swallows raw APC sequences, so any graphics
// command found in the "outside" half would never reach the terminal at all.
func unwrapTmux(s string) (inside, outside string) {
	const open = "\x1bPtmux;"
	var in, out strings.Builder
	for {
		i := strings.Index(s, open)
		if i < 0 {
			out.WriteString(s)
			return in.String(), out.String()
		}
		out.WriteString(s[:i])
		s = s[i+len(open):]
		j := 0
		for j < len(s) {
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == 0x1b {
				in.WriteByte(0x1b)
				j += 2
				continue
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				j += 2
				break
			}
			if s[j] == 0x1b {
				j++
				break
			}
			in.WriteByte(s[j])
			j++
		}
		s = s[j:]
	}
}

func parseAPCCommands(stream string) []apcCommand {
	var cmds []apcCommand
	s := stream
	for {
		i := strings.Index(s, "\x1b_G")
		if i < 0 {
			return cmds
		}
		s = s[i+3:]
		end := strings.Index(s, "\x1b\\")
		if end < 0 {
			return cmds
		}
		body := s[:end]
		s = s[end+2:]
		options, payload, _ := strings.Cut(body, ";")
		keys := map[string]string{}
		for _, option := range strings.Split(options, ",") {
			if option == "" {
				continue
			}
			k, v, ok := strings.Cut(option, "=")
			if ok {
				keys[k] = v
			}
		}
		cmds = append(cmds, apcCommand{keys: keys, payload: payload, raw: body})
	}
}

// Feed consumes one write from the manager, exactly as a terminal would.
func (t *termModel) Feed(stream string, envelope Envelope) error {
	t.bytesSeen += len(stream)
	if !utf8.ValidString(stream) {
		return fmt.Errorf("terminal received invalid UTF-8")
	}
	if envelope == TmuxPassthrough {
		inside, outside := unwrapTmux(stream)
		if cmds := parseAPCCommands(outside); len(cmds) > 0 {
			// A bare (unwrapped) APC under tmux is eaten by tmux and never
			// reaches the terminal: the image would silently not exist.
			return fmt.Errorf("unwrapped graphics command under tmux: %q", cmds[0].raw)
		}
		stream = inside
	}
	for _, cmd := range parseAPCCommands(stream) {
		t.apply(cmd)
	}
	return nil
}

func (t *termModel) apply(cmd apcCommand) {
	t.commands = append(t.commands, cmd)
	action := cmd.keys["a"]
	if action == "" {
		action = "t" // protocol default
	}
	id := parseUint32(cmd.keys["i"])
	switch action {
	case "t", "T":
		if id == 0 {
			// Continuation chunk of a chunked direct transmission.
			id = t.inFlight
		}
		if id == 0 {
			return
		}
		if cmd.keys["t"] == "t" {
			t.tempPaths[id] = cmd.payload
		}
		if cmd.keys["m"] == "1" {
			t.inFlight = id
		} else {
			t.inFlight = 0
		}
		t.images[id] = true
		if action == "T" {
			t.placements[placementHandle(id, parseUint32(cmd.keys["p"]))] = true
		}
	case "p":
		if id == 0 || cmd.keys["U"] != "1" {
			return
		}
		t.placements[placementHandle(id, parseUint32(cmd.keys["p"]))] = true
	case "d":
		switch strings.ToLower(cmd.keys["d"]) {
		case "i":
			delete(t.images, id)
			for handle := range t.placements {
				if uint32(handle>>32) == id {
					delete(t.placements, handle)
				}
			}
		case "a":
			t.placements = map[uint64]bool{}
			if cmd.keys["d"] == "A" {
				t.images = map[uint32]bool{}
			}
		}
	}
}

func (t *termModel) hasPlacement(p Placement) bool {
	return t.images[p.ImageID] && t.placements[placementHandle(p.ImageID, p.PlacementID)]
}

func parseUint32(s string) uint32 {
	var v uint64
	if s == "" {
		return 0
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		v = v*10 + uint64(s[i]-'0')
		if v > 0xffffffff {
			return 0
		}
	}
	return uint32(v)
}
