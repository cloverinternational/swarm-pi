// Source: terminaltexteffects/engine/base_character.py
// Covers: EventHandler (Event, Action, Callback, RegisterEvent), EffectCharacter
//
// Every input character becomes an EffectCharacter.
// It owns an Animation (appearance), a Motion (position), and an EventHandler
// that wires the two together with declarative event→action bindings.
// Tick() advances both systems by one step per frame.
package tteengine

import (
	"fmt"
	"sync/atomic"
)

// ---------------------------------------------------------------------------
// Unique ID counter — used by Path and Scene so EventHandler can key on int
// ---------------------------------------------------------------------------

var globalIDCounter int64

func nextID() int { return int(atomic.AddInt64(&globalIDCounter, 1)) }

// ---------------------------------------------------------------------------
// Event / Action enums
// Source: base_character.py — EventHandler.Event / EventHandler.Action
// ---------------------------------------------------------------------------

// EventType identifies what kind of state change fires an event.
type EventType int

const (
	EventSegmentEntered EventType = iota
	EventSegmentExited
	EventPathActivated
	EventPathComplete
	EventPathHolding
	EventSceneActivated
	EventSceneComplete
)

// ActionType identifies what to do when an event fires.
type ActionType int

const (
	ActionActivatePath ActionType = iota
	ActionActivateScene
	ActionDeactivatePath
	ActionDeactivateScene
	ActionResetAppearance
	ActionSetLayer
	ActionSetCoordinate
	ActionCallback
)

// ---------------------------------------------------------------------------
// EventHandler
// Source: base_character.py — EventHandler
// ---------------------------------------------------------------------------

// eventKey uniquely identifies an (event, callerID) pair.
// callerID is the integer ID assigned to a Path or Scene on creation.
type eventKey struct {
	event    EventType
	callerID int
}

// eventAction is an (action, target) pair stored per registered event.
type eventAction struct {
	action ActionType
	target any // *Path | *Scene | int | Coord | Callback
}

// Callback wraps a user function called when an event fires.
// Source: EventHandler.Callback
type Callback struct {
	Fn func(c *EffectCharacter)
}

// EventHandler wires motion/animation state transitions together.
type EventHandler struct {
	character        *EffectCharacter
	registeredEvents map[eventKey][]eventAction
}

// newEventHandler creates an EventHandler for the given character.
func newEventHandler(c *EffectCharacter) *EventHandler {
	return &EventHandler{
		character:        c,
		registeredEvents: make(map[eventKey][]eventAction),
	}
}

// callerID extracts the integer id from a caller object.
// Callers must be *Path, *Scene, or *Waypoint (only Path/Scene used in practice).
func callerID(caller any) int {
	switch v := caller.(type) {
	case *Path:
		return v.id
	case *Scene:
		return v.id
	default:
		return 0
	}
}

// RegisterEvent registers an action to take when a given event fires from a
// specific caller object (*Path or *Scene).
// Source: EventHandler.register_event
func (eh *EventHandler) RegisterEvent(event EventType, caller any, action ActionType, target any) {
	key := eventKey{event: event, callerID: callerID(caller)}
	eh.registeredEvents[key] = append(eh.registeredEvents[key], eventAction{action: action, target: target})
}

// handleEvent is called internally by Motion and Animation when state changes.
// Source: EventHandler._handle_event
func (eh *EventHandler) handleEvent(event EventType, caller any) {
	key := eventKey{event: event, callerID: callerID(caller)}
	actions, ok := eh.registeredEvents[key]
	if !ok {
		return
	}
	c := eh.character
	for _, ea := range actions {
		switch ea.action {
		case ActionActivatePath:
			if p, ok := ea.target.(*Path); ok {
				c.Motion.ActivatePath(p, eh)
			}
		case ActionActivateScene:
			if scn, ok := ea.target.(*Scene); ok {
				c.Animation.ActivateScene(scn)
				eh.handleEvent(EventSceneActivated, scn)
			}
		case ActionDeactivatePath:
			if p, ok := ea.target.(*Path); ok {
				c.Motion.DeactivatePath(p)
			}
		case ActionDeactivateScene:
			if scn, ok := ea.target.(*Scene); ok {
				c.Animation.DeactivateScene(scn)
			}
		case ActionResetAppearance:
			c.Animation.SetAppearance(c.InputSymbol(), ColorPair{})
		case ActionSetLayer:
			if layer, ok := ea.target.(int); ok {
				c.Layer = layer
			}
		case ActionSetCoordinate:
			if coord, ok := ea.target.(Coord); ok {
				c.Motion.SetCoordinate(coord)
			}
		case ActionCallback:
			if cb, ok := ea.target.(Callback); ok {
				cb.Fn(c)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// EffectCharacter
// Source: base_character.py — EffectCharacter
// ---------------------------------------------------------------------------

// EffectCharacter represents a single rune from the input text.
// It carries its own Animation, Motion, and EventHandler.
type EffectCharacter struct {
	id          int
	inputSymbol string
	inputCoord  Coord

	IsVisible bool
	Layer     int

	Animation    *Animation
	Motion       *Motion
	EventHandler *EventHandler
}

// newEffectCharacter creates a new EffectCharacter.
// Called exclusively by Terminal during input parsing.
func newEffectCharacter(id int, symbol string, col, row int) *EffectCharacter {
	coord := Coord{Column: col, Row: row}
	c := &EffectCharacter{
		id:          id,
		inputSymbol: symbol,
		inputCoord:  coord,
		Layer:       0,
	}
	c.Animation = NewAnimation(c)
	c.Motion = newMotion(coord)
	c.EventHandler = newEventHandler(c)
	return c
}

// ID returns the character's unique identifier.
func (c *EffectCharacter) ID() int { return c.id }

// InputSymbol returns the original symbol from the input text.
func (c *EffectCharacter) InputSymbol() string { return c.inputSymbol }

// InputCoord returns the character's home coordinate in the canvas.
func (c *EffectCharacter) InputCoord() Coord { return c.inputCoord }

// IsActive returns true if either motion or animation are still running.
// Source: EffectCharacter.is_active
func (c *EffectCharacter) IsActive() bool {
	return !c.Animation.ActiveSceneIsComplete() || !c.Motion.MovementIsComplete()
}

// Tick advances animation and motion by one step.
// Source: EffectCharacter.tick
func (c *EffectCharacter) Tick() {
	c.Motion.Move(c.EventHandler)
	c.Animation.StepAnimation()
}

// CurrentSymbol returns the symbol to render this tick.
func (c *EffectCharacter) CurrentSymbol() string {
	sym := c.Animation.CurrentVisual.Symbol
	if sym == "" {
		return c.inputSymbol
	}
	return sym
}

// FormattedSymbol returns the fully ANSI-formatted symbol for the current tick.
func (c *EffectCharacter) FormattedSymbol() string {
	cv := c.Animation.CurrentVisual
	if cv.Symbol == "" {
		cv.Symbol = c.inputSymbol
	}
	return cv.Formatted()
}

// String implements fmt.Stringer for debugging.
func (c *EffectCharacter) String() string {
	return fmt.Sprintf("EffectCharacter(id=%d symbol=%q col=%d row=%d)",
		c.id, c.inputSymbol, c.inputCoord.Column, c.inputCoord.Row)
}
