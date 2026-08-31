package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/interaction"
)

func TestQuestionBrokerUnattachedFailsFast(t *testing.T) {
	broker := NewQuestionBroker()
	start := time.Now()

	_, err := broker.Request(context.Background(), interaction.QuestionRequest{
		ID:      "unattached",
		Timeout: 600,
	})
	if got := interaction.OutcomeOf(err); got != interaction.OutcomeInteractiveUnavailable {
		t.Fatalf("outcome = %q, want %q (error %v)", got, interaction.OutcomeInteractiveUnavailable, err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("unattached request took %v", elapsed)
	}
}

func TestQuestionBrokerAcceptedZeroTimeoutStopsOnExplicitParentCancellation(t *testing.T) {
	broker := NewQuestionBroker()
	dispatched := make(chan struct{}, 1)
	broker.SetDispatcher(func(msg tea.Msg) {
		if _, ok := msg.(questionRequestMsg); ok {
			dispatched <- struct{}{}
		}
	})
	parent, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := broker.Request(parent, interaction.QuestionRequest{ID: "accepted", Timeout: 0})
		result <- err
	}()
	<-dispatched
	cancel()

	select {
	case err := <-result:
		if got := interaction.OutcomeOf(err); got != interaction.OutcomeParentCanceled {
			t.Fatalf("outcome = %q, want %q (error %v)", got, interaction.OutcomeParentCanceled, err)
		}
	case <-time.After(time.Second):
		t.Fatal("zero-timeout accepted request did not stop on parent cancellation")
	}
	if pending := broker.GetPendingRequests(); len(pending) != 0 {
		t.Fatalf("canceled request remains pending: %#v", pending)
	}
	if err := broker.Respond("accepted", interaction.QuestionResponse{Answer: "late"}); err == nil {
		t.Fatal("canceled waiter was not removed")
	}
}

func TestQuestionBrokerAcceptedZeroTimeoutStopsOnCustomCauseCancellation(t *testing.T) {
	broker := NewQuestionBroker()
	dispatched := make(chan struct{}, 1)
	broker.SetDispatcher(func(msg tea.Msg) {
		if _, ok := msg.(questionRequestMsg); ok {
			dispatched <- struct{}{}
		}
	})
	parent, cancel := context.WithCancelCause(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := broker.Request(parent, interaction.QuestionRequest{ID: "custom-cause", Timeout: 0})
		result <- err
	}()
	<-dispatched
	cancel(errors.New("session shutdown"))
	select {
	case err := <-result:
		if got := interaction.OutcomeOf(err); got != interaction.OutcomeParentCanceled {
			t.Fatalf("outcome = %q, want %q (error %v)", got, interaction.OutcomeParentCanceled, err)
		}
	case <-time.After(time.Second):
		t.Fatal("custom-cause cancellation did not stop accepted unlimited question")
	}
	if pending := broker.GetPendingRequests(); len(pending) != 0 {
		t.Fatalf("canceled request remains pending: %#v", pending)
	}
	if err := broker.Respond("custom-cause", interaction.QuestionResponse{Answer: "late"}); err == nil {
		t.Fatal("canceled waiter was not removed")
	}
}

func TestQuestionBrokerDeadlineExpiredAtDispatchIsParentCanceled(t *testing.T) {
	broker := NewQuestionBroker()
	ctx, cancel := context.WithCancelCause(context.Background())
	broker.SetDispatcher(func(msg tea.Msg) {
		if _, ok := msg.(questionRequestMsg); ok {
			cancel(context.DeadlineExceeded)
		}
	})

	resp, err := broker.Request(ctx, interaction.QuestionRequest{ID: "expired-at-dispatch", Timeout: 1})
	if got := interaction.OutcomeOf(err); got != interaction.OutcomeParentCanceled {
		t.Fatalf("outcome = %q, want %q (error %v, response %#v)", got, interaction.OutcomeParentCanceled, err, resp)
	}
	if resp.Timeout {
		t.Fatal("framework deadline was reported as a user timeout")
	}
	if pending := broker.GetPendingRequests(); len(pending) != 0 {
		t.Fatalf("expired request remains pending: %#v", pending)
	}
}

func TestQuestionBrokerExplicitTimeoutIsNormalOutcome(t *testing.T) {
	broker := NewQuestionBroker()
	broker.SetDispatcher(func(tea.Msg) {})
	start := time.Now()

	resp, err := broker.Request(context.Background(), interaction.QuestionRequest{
		ID:      "short",
		Timeout: 1,
	})
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	if !resp.Timeout {
		t.Fatal("response must report explicit timeout")
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("timed out too early: %v", elapsed)
	}
}

func TestQuestionBrokerPreCanceledParentIsDistinct(t *testing.T) {
	broker := NewQuestionBroker()
	broker.SetDispatcher(func(tea.Msg) {
		t.Fatal("pre-canceled request must not be dispatched")
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := broker.Request(ctx, interaction.QuestionRequest{ID: "canceled", Timeout: 10})
	if got := interaction.OutcomeOf(err); got != interaction.OutcomeParentCanceled {
		t.Fatalf("outcome = %q, want %q (error %v)", got, interaction.OutcomeParentCanceled, err)
	}
}

func TestQuestionBrokerDispatchFailureIsDistinct(t *testing.T) {
	broker := NewQuestionBroker()
	broker.SetDispatcher(func(tea.Msg) {
		panic("program stopped")
	})

	_, err := broker.Request(context.Background(), interaction.QuestionRequest{ID: "failed", Timeout: 10})
	if got := interaction.OutcomeOf(err); got != interaction.OutcomeDeliveryFailure {
		t.Fatalf("outcome = %q, want %q (error %v)", got, interaction.OutcomeDeliveryFailure, err)
	}
	if pending := broker.GetPendingRequests(); len(pending) != 0 {
		t.Fatalf("failed request remains pending: %#v", pending)
	}
}

func TestQuestionBrokerSequentialPrompts(t *testing.T) {
	broker := NewQuestionBroker()
	broker.SetDispatcher(func(msg tea.Msg) {
		request := msg.(questionRequestMsg).request
		if err := broker.Respond(request.ID, interaction.QuestionResponse{Answer: request.ID}); err != nil {
			t.Error(err)
		}
	})

	for _, id := range []string{"first", "second"} {
		resp, err := broker.Request(context.Background(), interaction.QuestionRequest{ID: id, Timeout: 2})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Answer != id {
			t.Fatalf("answer = %q, want %q", resp.Answer, id)
		}
	}
}
