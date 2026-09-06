package chat

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

// harnessPlanForAppOptions compiles the explicitly selected interactive-TUI
// harness. An empty path leaves the existing non-harness construction untouched.
func harnessPlanForAppOptions(opts AppOptions) (*harness.Plan, bool, error) {
	path := strings.TrimSpace(opts.HarnessPath)
	if path == "" {
		return nil, false, nil
	}
	plan, err := harness.Compile(path)
	if err != nil {
		return nil, false, fmt.Errorf("compile interactive harness %q: %w", path, err)
	}
	return plan, opts.HarnessAllowYolo, nil
}

func newSDKIntegrationForAppOptions(
	providerName, model string,
	appOpts AppOptions,
	sdkOpts SDKIntegrationOptions,
) (*SDKIntegration, error) {
	plan, allowYolo, err := harnessPlanForAppOptions(appOpts)
	if err != nil {
		return nil, err
	}
	sdkOpts.HarnessPlan = plan
	sdkOpts.HarnessAllowYolo = allowYolo
	return NewSDKIntegrationWithOptions(providerName, model, sdkOpts)
}
