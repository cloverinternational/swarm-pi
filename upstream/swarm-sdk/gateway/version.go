package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"sort"
	"sync"
)

// assetVersion is a content hash of the embedded PWA (web/**). Because the
// assets are compiled into the binary via go:embed, a rebuild/redeploy of the
// gateway changes this hash. Clients poll /api/version and reload when it
// changes — this is the "push updates to clients" path without a build step or
// a separate CDN: rebuild the gateway, restart, and every connected PWA picks
// up the new shell on its next poll.
var (
	assetVersionOnce sync.Once
	assetVersionVal  string
)

// computeAssetVersion walks the embedded web FS in a stable order and returns a
// short hex digest over every file's path + bytes.
func computeAssetVersion() string {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return "unknown"
	}
	var paths []string
	_ = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		h.Write([]byte(p))
		if b, err := fs.ReadFile(sub, p); err == nil {
			h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// assetVersion returns the cached content hash, computing it once.
func assetVersion() string {
	assetVersionOnce.Do(func() { assetVersionVal = computeAssetVersion() })
	return assetVersionVal
}
