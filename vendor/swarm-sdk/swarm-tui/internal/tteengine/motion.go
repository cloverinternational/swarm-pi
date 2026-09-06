// Source: terminaltexteffects/engine/motion.py
// Covers: Waypoint, Segment, Path, Motion
//
// The motion system controls where a character *is* on the canvas each tick.
// A character has a Motion which manages named Paths.
// A Path is an ordered list of Waypoints; between consecutive waypoints the
// engine interpolates position (linear or Bézier) at the configured speed.
// Multiple paths can be chained via events; only one is active at a time.
package tteengine

import "fmt"

// ---------------------------------------------------------------------------
// Waypoint
// Source: motion.py — Waypoint
// ---------------------------------------------------------------------------

// Waypoint is a destination on a Path with an optional Bézier control point.
type Waypoint struct {
	ID             string
	Coord          Coord
	BezierControls []Coord // 0 = linear, 1 = quadratic, 2 = cubic
}

// ---------------------------------------------------------------------------
// Segment
// Source: motion.py — Segment
// ---------------------------------------------------------------------------

// Segment connects two consecutive Waypoints and caches the arc-length.
type Segment struct {
	Start    Waypoint
	End      Waypoint
	Distance float64 // pre-computed arc-length
	// event tracking
	EnterTriggered bool
	ExitTriggered  bool
}

// positionAt returns the FloatCoord at local progress t ∈ [0,1] along this
// segment, honouring any BezierControls on the End waypoint.
func (seg Segment) positionAt(t float64) FloatCoord {
	start := FromCoord(seg.Start.Coord)
	end := FromCoord(seg.End.Coord)
	if len(seg.End.BezierControls) > 0 {
		controls := make([]FloatCoord, len(seg.End.BezierControls))
		for i, c := range seg.End.BezierControls {
			controls[i] = FromCoord(c)
		}
		return BezierAtControls(start, controls, end, t)
	}
	return start.Lerp(end, t)
}

// ---------------------------------------------------------------------------
// Path
// Source: motion.py — Path
// ---------------------------------------------------------------------------

// Path is an ordered sequence of Waypoints traversed at a constant speed.
// An optional EasingFunc shapes the rate of progress over the total distance.
type Path struct {
	id       int        // unique integer for EventHandler keying
	ID       string     // human-readable name
	Speed    float64    // canvas cells per tick
	Ease     EasingFunc // nil = linear
	HoldTime int        // ticks to pause at the end before completing
	Loop     bool

	waypoints  []Waypoint
	waypointLU map[string]Waypoint
	segments   []Segment

	// origin segment: from current position to first waypoint (rebuilt on Activate)
	originSegment *Segment

	totalDistance float64
	currentStep   int
	maxSteps      int
	holdRemaining int

	lastDistanceReached float64 // used for SyncDistance
}

// newPath creates an empty Path.
func newPath(id string, speed float64, ease EasingFunc, holdTime int, loop bool) *Path {
	if speed <= 0 {
		speed = 1
	}
	return &Path{
		id:         nextID(),
		ID:         id,
		Speed:      speed,
		Ease:       ease,
		HoldTime:   holdTime,
		Loop:       loop,
		waypointLU: make(map[string]Waypoint),
	}
}

// NewWaypoint appends a Waypoint to the Path and recalculates the arc-length.
// Source: Path.new_waypoint
func (p *Path) NewWaypoint(coord Coord, id string, bezierControls ...Coord) Waypoint {
	if id == "" {
		id = fmt.Sprintf("%d", len(p.waypoints))
	}
	wp := Waypoint{ID: id, Coord: coord, BezierControls: bezierControls}
	p.waypoints = append(p.waypoints, wp)
	p.waypointLU[id] = wp
	// rebuild the last segment if we now have ≥2 waypoints
	if len(p.waypoints) >= 2 {
		prev := p.waypoints[len(p.waypoints)-2]
		seg := p.buildSegment(prev, wp)
		p.segments = append(p.segments, seg)
		p.totalDistance += seg.Distance
		p.maxSteps = max(int(p.totalDistance/p.Speed), 1)
	}
	return wp
}

// buildSegment computes the distance between two waypoints.
func (p *Path) buildSegment(start, end Waypoint) Segment {
	s := FromCoord(start.Coord)
	e := FromCoord(end.Coord)
	var dist float64
	if len(end.BezierControls) > 0 {
		controls := make([]FloatCoord, len(end.BezierControls))
		for i, c := range end.BezierControls {
			controls[i] = FromCoord(c)
		}
		dist = LengthOfBezierCurve(s, controls, e)
	} else {
		dist = LengthOfLine(s, e, true) // double row diff like Python
	}
	if dist == 0 {
		dist = 0.001 // guard against zero-length segments
	}
	return Segment{Start: start, End: end, Distance: dist}
}

// step advances the path by one step and returns the new FloatCoord.
// Source: Path.step
func (p *Path) step(eh *EventHandler) FloatCoord {
	if len(p.segments) == 0 {
		return FromCoord(p.waypoints[len(p.waypoints)-1].Coord)
	}
	if p.maxSteps == 0 || p.currentStep >= p.maxSteps || p.totalDistance == 0 {
		return FromCoord(p.segments[len(p.segments)-1].End.Coord)
	}
	p.currentStep++
	var progress float64
	if p.Ease != nil {
		progress = p.Ease(float64(p.currentStep) / float64(p.maxSteps))
	} else {
		progress = float64(p.currentStep) / float64(p.maxSteps)
	}
	distToTravel := progress * p.totalDistance
	p.lastDistanceReached = distToTravel

	for i := range p.segments {
		seg := &p.segments[i]
		if distToTravel <= seg.Distance {
			// fire enter event on first entry
			if !seg.EnterTriggered {
				seg.EnterTriggered = true
				if eh != nil {
					eh.handleEvent(EventSegmentEntered, seg.End)
				}
			}
			localT := distToTravel / seg.Distance
			if seg.Distance == 0 {
				localT = 0
			}
			return seg.positionAt(localT)
		}
		distToTravel -= seg.Distance
		if !seg.ExitTriggered {
			seg.ExitTriggered = true
			if eh != nil {
				eh.handleEvent(EventSegmentExited, seg.End)
			}
		}
	}
	// past all segments — return final waypoint
	return FromCoord(p.segments[len(p.segments)-1].End.Coord)
}

// ---------------------------------------------------------------------------
// Motion
// Source: motion.py — Motion
// ---------------------------------------------------------------------------

// Motion manages movement of a single EffectCharacter.
// Paths are stored by ID; only one is active at a time.
type Motion struct {
	paths      map[string]*Path
	ActivePath *Path

	CurrentCoord  FloatCoord
	PreviousCoord FloatCoord
	home          Coord
}

// NewMotionForSpotlight creates a standalone Motion not tied to an EffectCharacter.
// Used by effects like Spotlights that move virtual light sources rather than text chars.
func NewMotionForSpotlight(home Coord) *Motion {
	return newMotion(home)
}

// SpotlightMover is a self-contained bezier-path follower for virtual spotlight sources.
// It manages a looping sequence of bezier paths without needing an EffectCharacter
// or EventHandler (avoids the nil-character issue in handleEvent).
type SpotlightMover struct {
	motion      *Motion
	searchPaths []*Path // looping search paths
	centerPath  *Path   // convergence path
	curPathIdx  int
	searching   bool
}

// NewSpotlightMover creates a SpotlightMover starting at home.
func NewSpotlightMover(home Coord) *SpotlightMover {
	return &SpotlightMover{
		motion:    newMotion(home),
		searching: true,
	}
}

// CurrentCoord returns the current floating-point position.
func (s *SpotlightMover) CurrentCoord() FloatCoord {
	return s.motion.CurrentCoord
}

// AddSearchPath appends a bezier path (with one bezier control point) to the
// search loop. coord is the waypoint destination; ctrlCoord is the bezier control.
func (s *SpotlightMover) AddSearchPath(coord Coord, ctrlCoord Coord, speed float64) {
	id := fmt.Sprintf("search_%d", len(s.searchPaths))
	p := s.motion.NewPath(id, speed, EaseInOutQuad, 0, false)
	p.NewWaypoint(coord, "wp", ctrlCoord)
	s.searchPaths = append(s.searchPaths, p)
}

// SetCenterPath creates the convergence path toward canvas center.
func (s *SpotlightMover) SetCenterPath(center Coord) {
	p := s.motion.NewPath("center", 0.5, EaseInOutSine, 0, false)
	p.NewWaypoint(center, "center")
	s.centerPath = p
}

// StartSearch activates the first search path.
func (s *SpotlightMover) StartSearch() {
	if len(s.searchPaths) > 0 {
		s.curPathIdx = 0
		s.motion.ActivatePath(s.searchPaths[0], nil)
	}
	s.searching = true
}

// StartConverge switches the mover to follow the center path.
func (s *SpotlightMover) StartConverge() {
	s.searching = false
	if s.centerPath != nil {
		s.motion.ActivatePath(s.centerPath, nil)
	}
}

// IsConvergeComplete returns true when the convergence path has finished.
func (s *SpotlightMover) IsConvergeComplete() bool {
	return !s.searching && s.motion.MovementIsComplete()
}

// Advance moves the spotlight one step, handling looping of search paths internally.
func (s *SpotlightMover) Advance() {
	if s.searching {
		if s.motion.MovementIsComplete() || s.motion.ActivePath == nil {
			// Advance to next search path (wrapping).
			if len(s.searchPaths) > 0 {
				s.curPathIdx = (s.curPathIdx + 1) % len(s.searchPaths)
				s.motion.ActivatePath(s.searchPaths[s.curPathIdx], nil)
			}
		}
		s.motion.Move(nil)
	} else {
		if !s.motion.MovementIsComplete() {
			s.motion.Move(nil)
		}
	}
}

// newMotion creates a Motion starting at home.
func newMotion(home Coord) *Motion {
	fc := FromCoord(home)
	return &Motion{
		paths:        make(map[string]*Path),
		CurrentCoord: fc,
		home:         home,
	}
}

// SetCoordinate teleports the character to coord instantly.
// Source: Motion.set_coordinate
func (m *Motion) SetCoordinate(coord Coord) {
	m.CurrentCoord = FromCoord(coord)
}

// NewPath creates a new named Path and registers it.
// Source: Motion.new_path
func (m *Motion) NewPath(id string, speed float64, ease EasingFunc, holdTime int, loop bool) *Path {
	if id == "" {
		id = fmt.Sprintf("path_%d", len(m.paths))
	}
	p := newPath(id, speed, ease, holdTime, loop)
	m.paths[id] = p
	return p
}

// QueryPath returns the Path with the given id, or nil.
func (m *Motion) QueryPath(id string) *Path {
	return m.paths[id]
}

// MovementIsComplete returns true when no path is active.
// Source: Motion.movement_is_complete
func (m *Motion) MovementIsComplete() bool {
	return m.ActivePath == nil
}

// ActivatePath starts traversal of the named path from the current position.
// It rebuilds the origin segment so characters always start from exactly
// where they currently are, regardless of where the path was originally built.
// Source: Motion.activate_path
func (m *Motion) ActivatePath(path *Path, eh *EventHandler) {
	if path == nil || len(path.waypoints) == 0 {
		return
	}
	m.ActivePath = path

	// Build an origin segment from the current coord to the first waypoint
	originWP := Waypoint{ID: "origin", Coord: m.CurrentCoord.ToCoord()}
	originSeg := path.buildSegment(originWP, path.waypoints[0])

	// Replace the previously-stored origin segment if any
	if path.originSegment != nil {
		// remove old origin segment from the front
		path.segments = path.segments[1:]
		path.totalDistance -= path.originSegment.Distance
	}
	path.originSegment = &originSeg
	path.segments = append([]Segment{originSeg}, path.segments...)
	path.totalDistance += originSeg.Distance

	// Reset traversal state
	path.currentStep = 0
	path.holdRemaining = path.HoldTime
	path.lastDistanceReached = 0
	if path.Speed > 0 {
		path.maxSteps = int(path.totalDistance / path.Speed)
	}
	if path.maxSteps < 1 {
		path.maxSteps = 1
	}
	for i := range path.segments {
		path.segments[i].EnterTriggered = false
		path.segments[i].ExitTriggered = false
	}

	if eh != nil {
		eh.handleEvent(EventPathActivated, path)
	}
}

// DeactivatePath clears the active path if it matches path.
// Source: Motion.deactivate_path
func (m *Motion) DeactivatePath(path *Path) {
	if m.ActivePath == path {
		m.ActivePath = nil
	}
}

// ChainPaths registers PATH_COMPLETE → ACTIVATE_PATH events between consecutive
// paths so they play in sequence automatically.
// Source: Motion.chain_paths
func (m *Motion) ChainPaths(eh *EventHandler, paths []*Path, loop bool) {
	for i := 1; i < len(paths); i++ {
		prev := paths[i-1]
		next := paths[i]
		eh.RegisterEvent(EventPathComplete, prev, ActionActivatePath, next)
	}
	if loop && len(paths) >= 2 {
		eh.RegisterEvent(EventPathComplete, paths[len(paths)-1], ActionActivatePath, paths[0])
	}
}

// Move advances the character along the active path by one step.
// This is called by EffectCharacter.Tick().
// Source: Motion.move
func (m *Motion) Move(eh *EventHandler) {
	m.PreviousCoord = m.CurrentCoord
	if m.ActivePath == nil || len(m.ActivePath.segments) == 0 {
		return
	}
	p := m.ActivePath
	m.CurrentCoord = p.step(eh)

	if p.currentStep < p.maxSteps {
		return
	}

	// Path reached its end
	if p.holdRemaining == p.HoldTime && p.HoldTime > 0 {
		if eh != nil {
			eh.handleEvent(EventPathHolding, p)
		}
		p.holdRemaining--
		return
	}
	if p.holdRemaining > 0 {
		p.holdRemaining--
		return
	}

	if p.Loop {
		looping := m.ActivePath
		m.DeactivatePath(m.ActivePath)
		m.ActivatePath(looping, eh)
	} else {
		completed := m.ActivePath
		m.DeactivatePath(m.ActivePath)
		if eh != nil {
			eh.handleEvent(EventPathComplete, completed)
		}
	}
}
