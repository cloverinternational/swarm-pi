package termimage

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

const (
	queryDirectID   = 31
	queryTmuxID     = 32
	queryTempID     = 33
	queryTmuxTempID = 34
)

// Response is a parsed Kitty graphics acknowledgement.
type Response struct {
	ID      uint32
	Message string
	OK      bool
}

// KittyQuery is the official direct-transmission capability query.
func KittyQuery() string { return directQuery(queryDirectID) }

// Query is an alias for KittyQuery.
func Query() string { return KittyQuery() }

func directQuery(id uint32) string {
	payload := base64.StdEncoding.EncodeToString([]byte{0, 0, 0})
	return ansi.KittyGraphics([]byte(payload), "a=q", "i="+strconv.FormatUint(uint64(id), 10),
		"f=24", "s=1", "v=1", "t=d")
}

func tempQuery(id uint32, path string) string {
	payload := base64.StdEncoding.EncodeToString([]byte(path))
	return ansi.KittyGraphics([]byte(payload), "a=q", "i="+strconv.FormatUint(uint64(id), 10),
		"f=24", "s=1", "v=1", "t=t")
}

// ParseResponse recognizes either a framed Kitty response or a body whose APC
// framing was already removed by the terminal input parser.
func ParseResponse[T ~string | ~[]byte](response T) (Response, bool) {
	s := string(response)
	for {
		start := strings.Index(s, "\x1b_G")
		if start < 0 {
			break
		}
		s = s[start+3:]
		end := strings.Index(s, "\x1b\\")
		if end < 0 {
			return Response{}, false
		}
		if parsed, ok := parseResponseBody(s[:end]); ok {
			return parsed, true
		}
		s = s[end+2:]
	}
	return parseResponseBody(strings.TrimSpace(string(response)))
}

func parseResponseBody(body string) (Response, bool) {
	options, message, ok := strings.Cut(body, ";")
	if !ok {
		return Response{}, false
	}
	var id uint64
	found := false
	for _, option := range strings.Split(options, ",") {
		key, value, present := strings.Cut(strings.TrimSpace(option), "=")
		if !present || key != "i" {
			continue
		}
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil || parsed == 0 {
			return Response{}, false
		}
		id, found = parsed, true
	}
	if !found {
		return Response{}, false
	}
	message = strings.TrimSpace(message)
	return Response{ID: uint32(id), Message: message, OK: message == "OK"}, true
}

// ParseKittyResponse preserves the old capability-query predicate.
func ParseKittyResponse[T ~string | ~[]byte](response T) bool {
	s := string(response)
	for {
		start := strings.Index(s, "\x1b_G")
		if start < 0 {
			break
		}
		s = s[start+3:]
		end := strings.Index(s, "\x1b\\")
		if end < 0 {
			return false
		}
		if parsed, ok := parseResponseBody(s[:end]); ok && parsed.ID == queryDirectID && parsed.OK {
			return true
		}
		s = s[end+2:]
	}
	parsed, ok := parseResponseBody(strings.TrimSpace(string(response)))
	return ok && parsed.ID == queryDirectID && parsed.OK
}

// CapabilityFromResponse converts a direct query response into a capability.
func CapabilityFromResponse[T ~string | ~[]byte](response T) Capability {
	if ParseKittyResponse(response) {
		return Kitty
	}
	return Unsupported
}

// CapabilityFromEnv returns an explicit terminal-image override.
func CapabilityFromEnv() (Capability, bool) {
	for _, name := range []string{
		"SWARM_TERMIMAGE", "SWARM_TERM_IMAGE_PROTOCOL", "SWARM_TUI_IMAGE_PROTOCOL", "TERM_IMAGE_PROTOCOL",
	} {
		if value, ok := os.LookupEnv(name); ok {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "kitty", "1", "true", "yes", "on":
				return Kitty, true
			case "blocks", "block", "unsupported", "none", "0", "false", "no", "off":
				return Unsupported, true
			default:
				return Unknown, true
			}
		}
	}
	return Unknown, false
}

// WrapTmuxPassthrough wraps one complete control sequence for tmux and doubles
// embedded ESC bytes as required by tmux's DCS passthrough contract.
func WrapTmuxPassthrough(sequence string) string {
	return "\x1bPtmux;" + strings.ReplaceAll(sequence, "\x1b", "\x1b\x1b") + "\x1b\\"
}

func wrapForEnvelope(sequence string, envelope Envelope) string {
	if envelope == TmuxPassthrough {
		return WrapTmuxPassthrough(sequence)
	}
	return sequence
}

// maxPlaceholderCells is the size of Kitty's rowcolumn-diacritics table. A row
// or column index at or beyond it cannot be expressed, and kitty.Diacritic
// silently substitutes the index-0 diacritic instead of failing, so every cell
// past the limit would claim to be row 0 / column 0. The terminal then reserves
// the full block of rows and paints nothing into them — the user sees "the
// image is blank but the space where it should be is there".
const maxPlaceholderCells = 297

// PlaceholderLines renders a virtual placement as ordinary one-cell Unicode
// graphemes. The full row, column, and high image-ID byte are encoded on every
// cell so clipping or wrapping cannot depend on a hidden predecessor. A
// geometry the diacritic table cannot address is rejected outright so the
// caller falls back to a text description instead of reserving dead space.
func PlaceholderLines(p Placement) []string {
	if p.Columns <= 0 || p.Rows <= 0 || p.ImageID == 0 || p.PlacementID == 0 {
		return nil
	}
	if p.Columns > maxPlaceholderCells || p.Rows > maxPlaceholderCells {
		return nil
	}
	imageColor := sgrColor(38, p.ImageID&0x00ffffff)
	placementColor := sgrColor(58, p.PlacementID&0x00ffffff)
	high := int((p.ImageID >> 24) & 0xff)
	lines := make([]string, p.Rows)
	for row := range p.Rows {
		var b strings.Builder
		b.Grow(p.Columns*16 + len(imageColor) + len(placementColor) + 10)
		b.WriteString(imageColor)
		b.WriteString(placementColor)
		for column := range p.Columns {
			b.WriteRune(kitty.Placeholder)
			b.WriteRune(kitty.Diacritic(row))
			b.WriteRune(kitty.Diacritic(column))
			b.WriteRune(kitty.Diacritic(high))
		}
		b.WriteString("\x1b[39m\x1b[59m")
		lines[row] = b.String()
	}
	return lines
}

func sgrColor(selector int, value uint32) string {
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", selector,
		(value>>16)&0xff, (value>>8)&0xff, value&0xff)
}
