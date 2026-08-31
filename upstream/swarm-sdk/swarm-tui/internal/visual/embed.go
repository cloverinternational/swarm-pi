package visual

import "embed"

//go:embed templates
var templatesFS embed.FS

// TemplatesFS exposes the embedded static asset tree for server use.
func TemplatesFS() embed.FS { return templatesFS }
