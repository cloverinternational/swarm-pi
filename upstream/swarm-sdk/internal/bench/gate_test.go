package bench

import "testing"

// TestObservationalHooksEnabled_DefaultsOff is the "nothing on by default"
// non-negotiable (PLAN.md §9.3). It runs with a cleared environment and no
// programmatic override, i.e. the state of a normal process.
func TestObservationalHooksEnabled_DefaultsOff(t *testing.T) {
	t.Setenv(EnvObservationalHooks, "")
	restore := SetObservationalHooksEnabled(false)
	restore() // drop the programmatic override so we test the true default

	if ObservationalHooksEnabled() {
		t.Fatalf("observational hooks must default to OFF")
	}
	status := ObservationalHooksStatus()
	if status.Enabled {
		t.Fatalf("status disagrees with Enabled(): %+v", status)
	}
	if status.Source != SourceDefault {
		t.Fatalf("unset environment should report SourceDefault, got %q", status.Source)
	}
}

// TestObservationalHooksEnabled_EnvSpellings pins exactly which values opt in.
// Garbage must be OFF: a typo in an env var must never silently enable
// instrumentation.
func TestObservationalHooksEnabled_EnvSpellings(t *testing.T) {
	cases := map[string]bool{
		"1":       true,
		"true":    true,
		"TRUE":    true,
		"True":    true,
		"yes":     true,
		"on":      true,
		" on ":    true,
		"":        false,
		"0":       false,
		"false":   false,
		"off":     false,
		"no":      false,
		"maybe":   false,
		"enabled": false, // deliberately NOT recognised
		"2":       false,
	}
	for raw, want := range cases {
		t.Run("value="+raw, func(t *testing.T) {
			t.Setenv(EnvObservationalHooks, raw)
			restore := SetObservationalHooksEnabled(false)
			restore()

			if got := ObservationalHooksEnabled(); got != want {
				t.Fatalf("value %q: got enabled=%v want %v", raw, got, want)
			}
			status := ObservationalHooksStatus()
			if status.EnvValue != raw {
				t.Fatalf("status must report the raw env value for diagnosis: got %q want %q", status.EnvValue, raw)
			}
			wantSource := SourceDefault
			if want {
				wantSource = SourceEnv
			}
			if status.Source != wantSource {
				t.Fatalf("value %q: got source %q want %q", raw, status.Source, wantSource)
			}
		})
	}
}

// TestObservationalHooksEnabled_ProgrammaticOverridesEnvBothWays asserts an
// embedder can force the gate in either direction. Forcing OFF matters most: a
// host that must guarantee no instrumentation cannot be overridden by whatever
// happens to be in its environment.
func TestObservationalHooksEnabled_ProgrammaticOverridesEnvBothWays(t *testing.T) {
	t.Run("force_on_over_empty_env", func(t *testing.T) {
		t.Setenv(EnvObservationalHooks, "")
		restore := SetObservationalHooksEnabled(true)
		defer restore()

		if !ObservationalHooksEnabled() {
			t.Fatalf("programmatic true must enable")
		}
		if got := ObservationalHooksStatus().Source; got != SourceProgrammatic {
			t.Fatalf("source should be programmatic, got %q", got)
		}
	})

	t.Run("force_off_over_enabling_env", func(t *testing.T) {
		t.Setenv(EnvObservationalHooks, "1")
		restore := SetObservationalHooksEnabled(false)
		defer restore()

		if ObservationalHooksEnabled() {
			t.Fatalf("programmatic false must win over an enabling environment")
		}
		status := ObservationalHooksStatus()
		if status.Source != SourceProgrammatic {
			t.Fatalf("source should be programmatic, got %q", status.Source)
		}
		if status.EnvValue != "1" {
			t.Fatalf("status should still disclose the overridden env value, got %q", status.EnvValue)
		}
	})
}

// TestSetObservationalHooksEnabled_RestoreIsExact asserts the restore function
// returns the package to its previous state, including the never-set state.
// Without this, one test enabling the gate would leak into every later test in
// the process — which is exactly how a default-off feature ends up on.
func TestSetObservationalHooksEnabled_RestoreIsExact(t *testing.T) {
	t.Setenv(EnvObservationalHooks, "")

	if ObservationalHooksStatus().Source != SourceDefault {
		t.Fatalf("precondition: expected a pristine default state")
	}

	restoreOuter := SetObservationalHooksEnabled(true)
	if !ObservationalHooksEnabled() {
		t.Fatalf("outer set did not take effect")
	}

	restoreInner := SetObservationalHooksEnabled(false)
	if ObservationalHooksEnabled() {
		t.Fatalf("inner set did not take effect")
	}
	restoreInner()
	if !ObservationalHooksEnabled() {
		t.Fatalf("inner restore should have returned to the outer value (true)")
	}

	restoreOuter()
	if ObservationalHooksEnabled() {
		t.Fatalf("outer restore should have returned to the default (false)")
	}
	if got := ObservationalHooksStatus().Source; got != SourceDefault {
		t.Fatalf("outer restore should have returned to the never-set state, got source %q", got)
	}
}
