package toolrender

// Registry holds all registered renderers, checked in priority order.
type Registry struct {
	renderers []Renderer
	fallback  Renderer
}

// NewRegistry creates a registry with all built-in renderers pre-registered.
// Renderers are checked in registration order (first match wins).
// NOTE: This will import and register all subpackage renderers.
// For now, just create the structure — renderers will be registered later.
func NewRegistry() *Registry {
	r := &Registry{}
	// Renderers will be registered by the caller (App init)
	return r
}

// Register adds a renderer. First registered = highest priority.
func (r *Registry) Register(rend Renderer) {
	r.renderers = append(r.renderers, rend)
}

// SetFallback sets the fallback renderer (used when no CanRender matches).
func (r *Registry) SetFallback(rend Renderer) {
	r.fallback = rend
}

// Render finds the matching renderer and returns styled output lines.
func (r *Registry) Render(ctx *RenderContext, cached CachedResult) []string {
	for _, rend := range r.renderers {
		if rend.CanRender(ctx) {
			return rend.Render(ctx, cached)
		}
	}
	if r.fallback != nil {
		return r.fallback.Render(ctx, cached)
	}
	return nil
}

// PreProcess finds the matching renderer and pre-computes cached results.
func (r *Registry) PreProcess(ctx *RenderContext) CachedResult {
	for _, rend := range r.renderers {
		if rend.CanRender(ctx) {
			return rend.PreProcess(ctx)
		}
	}
	if r.fallback != nil {
		return r.fallback.PreProcess(ctx)
	}
	return nil
}

// Find returns the first renderer that can handle this context, or fallback.
func (r *Registry) Find(ctx *RenderContext) Renderer {
	for _, rend := range r.renderers {
		if rend.CanRender(ctx) {
			return rend
		}
	}
	return r.fallback
}
