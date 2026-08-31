# INDEX.md — x11

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./x11`

---

## Scope

### `pkg/computeruse/x11` — X11 `Backend` providing mouse, keyboard, clipboard, display, and app control via xdotool


---

## Árbol de Estructura

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
