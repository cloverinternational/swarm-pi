package builtin

import "testing"

// TestSanitizeProviderName guards the sub-agent provider_mismatch fix.
//
// The current-provider getter / some resolvers can hand back a decorated
// orchestrator-endpoint label like "anthropic/claude-opus-4-8[0]"
// (provider/model[index]). If that lands in provider.Config.Name, the factory's
// ProvidersMatch gate fails with:
//
//	"definition requires provider 'claudecode' but got 'anthropic/claude-opus-4-8[0]'"
//
// killing every builtin/custom sub-agent spawn. sanitizeProviderName must reduce
// any such label to the bare provider name.
func TestSanitizeProviderName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"anthropic/claude-opus-4-8[0]", "anthropic"},
		{"anthropic", "anthropic"},
		{"claudecode", "claudecode"},
		{"openai/gpt-4o[2]", "openai"},
		{"z.ai", "z.ai"}, // dotted provider names must survive (no '/' or '[')
		{"  anthropic/claude[0]  ", "anthropic"},
		{"anthropic[0]", "anthropic"},
		{"", ""},
	}
	for _, c := range cases {
		if got := sanitizeProviderName(c.in); got != c.want {
			t.Errorf("sanitizeProviderName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
