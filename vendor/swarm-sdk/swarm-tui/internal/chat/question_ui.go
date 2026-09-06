package chat

import (
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/interaction"
)

func (a *App) enqueueQuestionRequest(req interaction.QuestionRequest) {
	logDebug("[QUESTION] Question request enqueued: id=%s type=%s", req.ID, req.Type)
	a.questionQueue = append(a.questionQueue, req)
	if a.questionModal == nil {
		logDebug("[QUESTION] No modal showing, displaying question")
		a.showNextQuestion()
		return
	}
	logDebug("[QUESTION] Modal already showing, adding to queue (position: %d of %d)", 1, len(a.questionQueue))
	a.questionModal.SetQueueInfo(1, len(a.questionQueue))
}

func (a *App) removeQuestionRequest(requestID string, timedOut bool) {
	if len(a.questionQueue) == 0 {
		return
	}

	idx := -1
	for i, req := range a.questionQueue {
		if req.ID == requestID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return
	}

	a.questionQueue = append(a.questionQueue[:idx], a.questionQueue[idx+1:]...)
	if idx == 0 {
		a.questionModal = nil
		a.showNextQuestion()
	} else if a.questionModal != nil {
		a.questionModal.SetQueueInfo(1, len(a.questionQueue))
	}

	if timedOut {
		a.addNotification("warning", "Question timed out")
	}
}

func (a *App) showNextQuestion() {
	if len(a.questionQueue) == 0 {
		a.questionModal = nil
		return
	}

	req := a.questionQueue[0]
	a.questionModal = NewQuestionModal(req, 1, len(a.questionQueue),
		func(resp interaction.QuestionResponse) {
			a.handleQuestionResponse(resp)
		},
		func() {
			a.handleQuestionCancel()
		},
	)
}

func (a *App) handleQuestionResponse(resp interaction.QuestionResponse) {
	if len(a.questionQueue) == 0 {
		a.questionModal = nil
		return
	}

	req := a.questionQueue[0]
	if a.questionBroker != nil {
		if err := a.questionBroker.Respond(req.ID, resp); err != nil {
			logDebug("failed to respond to question %s: %v", req.ID, err)
			a.addNotification("error", "Question response failed")
		}
	}

	a.questionQueue = a.questionQueue[1:]
	if len(a.questionQueue) == 0 {
		a.questionModal = nil
		return
	}

	a.showNextQuestion()
}

func (a *App) handleQuestionCancel() {
	if len(a.questionQueue) == 0 {
		a.questionModal = nil
		return
	}

	req := a.questionQueue[0]
	if a.questionBroker != nil {
		resp := interaction.QuestionResponse{
			Canceled:    true,
			RespondedAt: time.Now(),
		}
		if err := a.questionBroker.Respond(req.ID, resp); err != nil {
			logDebug("failed to cancel question %s: %v", req.ID, err)
		}
	}

	a.questionQueue = a.questionQueue[1:]
	if len(a.questionQueue) == 0 {
		a.questionModal = nil
		return
	}

	a.showNextQuestion()
}
