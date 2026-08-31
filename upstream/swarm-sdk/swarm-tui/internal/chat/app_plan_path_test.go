package chat

import (
	"os"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/plan"
)

func TestPlanSessionFilePathUsesCanonicalPlanLocation(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())
	app := &App{rootSessionID: "plan-session"}

	want := plan.PlanFilePath(plan.Config{SessionID: app.rootSessionID})
	if got := app.planSessionFilePath(); got != want {
		t.Fatalf("planSessionFilePath() = %q, want %q", got, want)
	}

	if err := plan.WritePlanFile(plan.Config{SessionID: app.rootSessionID}, "# Canonical plan"); err != nil {
		t.Fatal(err)
	}
	if got := app.readActivePlan(); got != "# Canonical plan" {
		t.Fatalf("readActivePlan() = %q", got)
	}

	if _, err := os.Stat(want); err != nil {
		t.Fatalf("canonical plan missing at %q: %v", want, err)
	}
}
