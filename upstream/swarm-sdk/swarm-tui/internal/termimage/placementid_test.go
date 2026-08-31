package termimage

import (
	"fmt"
	"testing"
)

// TestPlacementIDSurvivesEncoding pins the invariant behind the 24-bit mask in
// allocatePlacementID.
//
// Placement IDs reach the terminal inside an SGR underline color, which carries
// 24 bits. If the manager ever handed out an ID wider than that, the encoder's
// own mask would silently narrow it and the manager would hold an identity the
// terminal never saw — a later delete or re-place would then address a
// placement that does not exist. So the encoder's mask must be a no-op.
func TestPlacementIDSurvivesEncoding(t *testing.T) {
	m := NewManager()

	for i := range 512 {
		src := syntheticSource(fmt.Sprintf("source-%d", i))
		placement, err := m.Register(src, fmt.Sprintf("occurrence-%d", i), 2, 2)
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if placement.PlacementID == 0 {
			t.Fatal("placement ID 0 is reserved and must never be allocated")
		}
		if masked := placement.PlacementID & 0x00ffffff; masked != placement.PlacementID {
			t.Fatalf("placement ID %#x does not survive the 24-bit wire encoding (becomes %#x)",
				placement.PlacementID, masked)
		}
		// Image IDs are a separate 32-bit namespace: the low 24 bits ride in the
		// foreground color and the high byte in a diacritic, so they are NOT
		// required to fit in 24 bits.
		if placement.ImageID == 0 {
			t.Fatal("image ID 0 is reserved and must never be allocated")
		}
	}
}

// TestPlacementIDsAreDistinctPerLivePlacement guards the property that actually
// matters for correctness: two live placements must not share an identity.
// Placement IDs are allocated once per distinct (source, occurrence, columns,
// rows) and cached, so re-registering the same key must return the same ID
// rather than burning a new one.
func TestPlacementIDsAreDistinctPerLivePlacement(t *testing.T) {
	m := NewManager()
	src := syntheticSource("stable")

	seen := map[uint32]string{}
	for i := range 64 {
		occurrence := fmt.Sprintf("occurrence-%d", i)
		placement, err := m.Register(src, occurrence, 2, 2)
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if prev, dup := seen[placement.PlacementID]; dup {
			t.Fatalf("placement ID %#x reused by %q and %q",
				placement.PlacementID, prev, occurrence)
		}
		seen[placement.PlacementID] = occurrence

		// Re-registering the identical key must be idempotent.
		again, err := m.Register(src, occurrence, 2, 2)
		if err != nil {
			t.Fatalf("Register (repeat): %v", err)
		}
		if again.PlacementID != placement.PlacementID {
			t.Fatalf("re-registering %q allocated a new placement ID %#x (was %#x)",
				occurrence, again.PlacementID, placement.PlacementID)
		}
		if again.ImageID != placement.ImageID {
			t.Fatalf("re-registering %q changed the image ID: %#x -> %#x",
				occurrence, placement.ImageID, again.ImageID)
		}
	}
}
