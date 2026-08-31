# INDEX.md — voice

> Modo: incremental | Directorios indexados: 1
> Para regenerar: `idx generate --path ./voice`

---

## Scope

### `voice/` — Cross-platform audio capture via the `AudioCapture` interface


---

## Árbol de Estructura

### `voice/` — Cross-platform audio capture via the `AudioCapture` interface
Provides `NewAudioCapture` to create platform-appropriate capture instances, with `BaseCapture` embedding for shared state and a registry (`RegisterCapture`/`ListCaptures`) for OS-specific backends.
- `types.go` — core types and `AudioCapture` interface
- `capture_linux.go` — Linux capture backends (PipeWire/PulseAudio, ALSA)
- `capture_darwin.go` — macOS capture backends
**Entry:** `types.go:L1`

---

