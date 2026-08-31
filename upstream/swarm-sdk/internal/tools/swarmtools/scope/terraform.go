package scope

import "regexp"

// TerraformDetector detects scope boundaries in Terraform/HCL files.
// Tracks: resource, data, variable, output, module, locals, provider, terraform blocks.
type TerraformDetector struct{ *BraceDetector }

func init() {
	det := NewBraceDetector(BraceConfig{
		LangName:    "terraform",
		CountBraces: CStyleBraceCounter,
		Patterns: []PatternRule{
			// resource "type" "name"
			{
				Re:    regexp.MustCompile(`^\s*resource\s+"(\w+)"\s+"(\w+)"`),
				Label: func(m []string) string { return "resource " + m[1] + "." + m[2] },
			},
			// data "type" "name"
			{
				Re:    regexp.MustCompile(`^\s*data\s+"(\w+)"\s+"(\w+)"`),
				Label: func(m []string) string { return "data " + m[1] + "." + m[2] },
			},
			// variable "name"
			{
				Re:    regexp.MustCompile(`^\s*variable\s+"(\w+)"`),
				Label: func(m []string) string { return "variable " + m[1] },
			},
			// output "name"
			{
				Re:    regexp.MustCompile(`^\s*output\s+"(\w+)"`),
				Label: func(m []string) string { return "output " + m[1] },
			},
			// module "name"
			{
				Re:    regexp.MustCompile(`^\s*module\s+"(\w+)"`),
				Label: func(m []string) string { return "module " + m[1] },
			},
			// locals
			{
				Re:    regexp.MustCompile(`^\s*locals\s*\{`),
				Label: func(m []string) string { return "locals" },
			},
			// provider "name"
			{
				Re:    regexp.MustCompile(`^\s*provider\s+"(\w+)"`),
				Label: func(m []string) string { return "provider " + m[1] },
			},
			// terraform block
			{
				Re:    regexp.MustCompile(`^\s*terraform\s*\{`),
				Label: func(m []string) string { return "terraform" },
			},
			// dynamic block
			{
				Re:    regexp.MustCompile(`^\s*dynamic\s+"(\w+)"`),
				Label: func(m []string) string { return "dynamic " + m[1] },
			},
			// nested block (e.g. ingress, egress, tags)
			{
				Re:    regexp.MustCompile(`^\s*(\w+)\s*\{`),
				Label: func(m []string) string { return m[1] },
			},
		},
	})
	Register(det, ".tf", ".tfvars", ".hcl")
}
