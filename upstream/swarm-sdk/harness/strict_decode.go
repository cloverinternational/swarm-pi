package harness

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/configformat"
)

// strictDecode parses raw manifest bytes into a Document with maximal strictness:
//
//   - YAML is converted to canonical JSON via configformat.ToJSON first.
//   - Multiple YAML documents in one stream are rejected (no silent drop).
//   - json.Decoder.DisallowUnknownFields rejects unknown keys at every level.
//   - Trailing data after the first JSON value is rejected.
//
// It performs no resolution and no side effects; it only shapes bytes into a
// Document and reports structural diagnostics.
func strictDecode(sourcePath string, raw []byte) (*Document, Diagnostics) {
	format := configformat.FormatForPath(sourcePath)

	if format == configformat.FormatYAML && yamlHasMultipleDocuments(raw) {
		return nil, Diagnostics{newDiag("harness.decode.multipleDocuments", "",
			"manifest contains more than one YAML document; a harness file must contain exactly one", sourcePath)}
	}

	jsonBytes, err := configformat.ToJSON(raw, format)
	if err != nil {
		return nil, Diagnostics{newDiag("harness.decode.syntax", "",
			"manifest is not valid "+formatName(format), sourcePath)}
	}

	dec := json.NewDecoder(bytes.NewReader(jsonBytes))
	dec.DisallowUnknownFields()

	var doc Document
	if err := dec.Decode(&doc); err != nil {
		return nil, Diagnostics{decodeError(sourcePath, err)}
	}

	// Reject trailing data: a clean manifest yields exactly one top-level value.
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, Diagnostics{newDiag("harness.decode.trailingData", "",
			"unexpected trailing data after the harness document", sourcePath)}
	}

	return &doc, nil
}

// decodeError converts an encoding/json decode error into a structured
// diagnostic. Unknown-field errors are given a specific, actionable code.
func decodeError(sourcePath string, err error) Diagnostic {
	msg := err.Error()
	if strings.Contains(msg, "unknown field") {
		field := ""
		if i := strings.Index(msg, "unknown field "); i >= 0 {
			field = strings.Trim(msg[i+len("unknown field "):], "\"")
		}
		return newDiag("harness.decode.unknownField", field,
			"unknown field "+quote(field)+"; remove it or check spelling and nesting", sourcePath)
	}
	return newDiag("harness.decode.invalid", "",
		"manifest could not be decoded into a harness document", sourcePath)
}

// yamlHasMultipleDocuments reports whether a YAML byte stream contains more than
// one document. It counts documents that actually carry content, treating "---"
// and "..." marker lines as separators and ignoring blank/comment lines. This
// is standard-library only and intentionally conservative.
func yamlHasMultipleDocuments(raw []byte) bool {
	var docs int
	inDoc := false
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		rtrim := strings.TrimRight(line, " \t")
		if rtrim == "---" || rtrim == "..." {
			inDoc = false
			continue
		}
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if !inDoc {
			docs++
			inDoc = true
			if docs > 1 {
				return true
			}
		}
	}
	return docs > 1
}

func formatName(f configformat.Format) string {
	if f == configformat.FormatYAML {
		return "YAML"
	}
	return "JSON"
}
