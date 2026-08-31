package hooks

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/findings"
)

// GetFindingsCache returns the findings cache when enabled.
// Returns nil if findings are not enabled.
func (hm *HooksManager) GetFindingsCache() findings.Cache {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	if hm.localFindings == nil {
		return nil
	}
	return hm.localFindings.GetCache()
}
