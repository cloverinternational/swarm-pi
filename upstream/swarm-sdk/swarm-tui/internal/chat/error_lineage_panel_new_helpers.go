package chat

// rootCauseFrame returns the last frame in the chain (the deepest/root cause).
func (p *errorLineagePanel) rootCauseFrame() *errorLineageFrame {
	if p == nil || len(p.Frames) == 0 {
		return nil
	}
	return &p.Frames[len(p.Frames)-1]
}
