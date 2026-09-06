// Package effects contains all TTE effects built on internal/tteengine.
//
// Adding a new effect:
//  1. Create a new file in this package, e.g. effect_myeffect.go
//  2. Define a Config struct with your parameters and a DefaultConfig() func.
//  3. Define your effect struct embedding *tteengine.BaseIterator.
//  4. Implement tteengine.Effect: Name(), Init(t *tteengine.Terminal), Next(), Reset().
//  5. In Init(): call tteengine.NewBaseIterator(t), then build() your characters.
//  6. In Next(): drip-feed pending→active, call b.Update(), return b.Frame().
//
// See effect_rain.go for the canonical reference implementation.
package effects
