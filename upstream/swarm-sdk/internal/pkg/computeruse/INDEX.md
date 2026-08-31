# INDEX.md — computeruse

> Modo: incremental | Directorios indexados: 2
> Para regenerar: `idx generate --path ./computeruse`

---

## Scope

### `pkg/computeruse` — CoordinateConverter and error types for Linux computer-use automation


---

## Árbol de Estructura

### `pkg/computeruse` — CoordinateConverter and error types for Linux computer-use automation
Provides coordinate normalization, pixel conversion, clamping, animation easing, and typed display/input errors.
- `types.go` — CoordinateConverter struct and coordinate/animation helper functions
- `executor.go` — LinuxExecutor and DisplayServer constants
- `errors.go` — ToolNotAvailableError, DisplayError, InputError types
**Entry:** `types.go:L1`

### `pkg/computeruse/x11` — X11 `Backend` providing mouse, keyboard, clipboard, display, and app control via xdotool
Wraps common desktop automation operations behind a `Backend` type initialized with `NewBackend`.
- `backend.go` — defines `Backend`, `Options`, `NewBackend`, clipboard and display helpers
- `apps.go` — application introspection and launch (`GetFrontmostApp`, `ListRunningApps`, `OpenApp`)
- `mouse.go` — mouse movement and input simulation (`MoveMouse`, `Type`, `Key`)
**Entry:** `backend.go:L1`

---

## Señales de Cambio Reciente

- `swarm-sdk/pkg/computeruse/x11/display.go`
- `swarm-sdk/pkg/computeruse/x11/apps.go`
- `swarm-sdk/pkg/computeruse/x11/mouse.go`
