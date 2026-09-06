package tools

import (
	"fmt"
	"strings"
)

// XMLBuilder builds structured XML output for tool results.
// It uses fmt and strings only — no encoding/xml — so CDATA blocks are
// emitted verbatim without any unexpected escaping.
//
// Usage:
//
//	b := tools.NewXML("result").
//	    Attr("exit_code", "0").
//	    AttrInt("duration_ms", 42).
//	    Field("stdout", "hello world").
//	    Field("stderr", "")
//	return tools.NewXMLResult(b), nil
type XMLBuilder struct {
	root  string
	attrs []string
	body  strings.Builder
}

// NewXML creates a new XMLBuilder with the given root tag name.
func NewXML(root string) *XMLBuilder {
	return &XMLBuilder{root: root}
}

// Attr adds a string attribute to the root element: key="value".
func (x *XMLBuilder) Attr(k, v string) *XMLBuilder {
	x.attrs = append(x.attrs, fmt.Sprintf(`%s=%q`, k, v))
	return x
}

// AttrInt adds an integer attribute to the root element: key="N".
func (x *XMLBuilder) AttrInt(k string, v int64) *XMLBuilder {
	x.attrs = append(x.attrs, fmt.Sprintf(`%s="%d"`, k, v))
	return x
}

// AttrBool adds a boolean attribute to the root element: key="true"|"false".
func (x *XMLBuilder) AttrBool(k string, v bool) *XMLBuilder {
	if v {
		return x.Attr(k, "true")
	}
	return x.Attr(k, "false")
}

// Field adds a CDATA child element: <tag><![CDATA[value]]></tag>.
// Use this for any content that may contain <, >, or & characters such as
// command output, file contents, code bodies, or web content.
func (x *XMLBuilder) Field(tag, value string) *XMLBuilder {
	fmt.Fprintf(&x.body, "  <%s><![CDATA[%s]]></%s>\n", tag, value, tag)
	return x
}

// Child appends a pre-built raw XML string as a child of the root element.
// The caller is responsible for correct indentation and well-formedness.
func (x *XMLBuilder) Child(raw string) *XMLBuilder {
	x.body.WriteString(raw)
	return x
}

// SelfClose appends a self-closing child element: <tag attr1="v1" attr2="v2"/>.
// Pass additional key="value" attribute strings in attrs.
func (x *XMLBuilder) SelfClose(tag string, attrs ...string) *XMLBuilder {
	if len(attrs) == 0 {
		fmt.Fprintf(&x.body, "  <%s/>\n", tag)
	} else {
		fmt.Fprintf(&x.body, "  <%s %s/>\n", tag, strings.Join(attrs, " "))
	}
	return x
}

// Build assembles the final XML string.
// If the body is empty the root element is self-closed: <root attr="v"/>.
// Otherwise it produces:
//
//	<root attr="v">
//	  ...children...
//	</root>
func (x *XMLBuilder) Build() string {
	attrStr := ""
	if len(x.attrs) > 0 {
		attrStr = " " + strings.Join(x.attrs, " ")
	}
	body := x.body.String()
	if body == "" {
		return fmt.Sprintf("<%s%s/>", x.root, attrStr)
	}
	return fmt.Sprintf("<%s%s>\n%s</%s>", x.root, attrStr, body, x.root)
}
