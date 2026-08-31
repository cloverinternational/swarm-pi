package chat

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
)

const (
	liveEvalBaseURL = "http://localhost:8082/v1"
	liveEvalModel   = "nvidia/diffusiongemma-26B-A4B-it-NVFP4"
)

// TestGoalEvaluatorLive runs evaluateGoalWithLLM against the local vLLM
// server exactly as the TUI wires it — same provider construction, same
// model. Opt-in via SWARM_LIVE_EVAL=1 (and skipped when the server is
// unreachable) so routine sweeps never make nondeterministic LLM calls:
//
//	SWARM_LIVE_EVAL=1 go test -run TestGoalEvaluatorLive ./internal/chat/ -v
func TestGoalEvaluatorLive(t *testing.T) {
	if os.Getenv("SWARM_LIVE_EVAL") != "1" {
		t.Skip("live evaluator test is opt-in: set SWARM_LIVE_EVAL=1")
	}
	httpClient := &http.Client{Timeout: 2 * time.Second}
	if resp, err := httpClient.Get(liveEvalBaseURL + "/models"); err != nil {
		t.Skipf("local vLLM not reachable (%v) — skipping live evaluator test", err)
	} else {
		resp.Body.Close()
	}

	prov, err := openai.New(openai.Config{
		APIKey:  "vllm",
		BaseURL: liveEvalBaseURL,
		Name:    "local",
	})
	if err != nil {
		t.Fatalf("provider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Case 1: condition clearly satisfied by the transcript → MET.
	met, err := evaluateGoalWithLLM(ctx, prov, liveEvalModel,
		"the assistant said the word banana",
		"User: say banana\n\nAssistant: banana")
	if err != nil {
		t.Fatalf("evaluate (met case): %v", err)
	}
	t.Logf("MET case verdict: ok=%v reason=%q", met.Ok, met.Reason)
	if !met.Ok {
		t.Errorf("expected MET, got not-met (reason: %s)", met.Reason)
	}

	// Case 2: condition clearly NOT satisfied → NOT_MET.
	notMet, err := evaluateGoalWithLLM(ctx, prov, liveEvalModel,
		"the file /tmp/goal-test.txt was created and contains the word done",
		"User: create the file\n\nAssistant: I have not created any files yet, I was searching the codebase.")
	if err != nil {
		t.Fatalf("evaluate (not-met case): %v", err)
	}
	t.Logf("NOT_MET case verdict: ok=%v impossible=%v reason=%q", notMet.Ok, notMet.Impossible, notMet.Reason)
	if notMet.Ok {
		t.Errorf("expected NOT_MET, got met (reason: %s)", notMet.Reason)
	}
	if notMet.Impossible {
		t.Errorf("expected NOT_MET, got impossible (reason: %s)", notMet.Reason)
	}
}
