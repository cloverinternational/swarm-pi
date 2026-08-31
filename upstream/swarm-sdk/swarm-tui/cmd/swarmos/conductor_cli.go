package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conductor"
)

// runConductorCLI implements `swarmos conductor [WORKFLOW.md]`.
//
// It loads the workflow file, builds a client.Client from the user's stored
// config, wires a Conductor, starts the poll loop, and blocks until SIGINT or
// SIGTERM.
//
// Usage:
//
//	swarmos conductor                  # uses ./WORKFLOW.md
//	swarmos conductor /path/WORKFLOW.md
func runConductorCLI(args []string) error {
	workflowPath := "WORKFLOW.md"
	if len(args) > 0 && args[0] != "" {
		workflowPath = args[0]
	}

	// Validate the workflow file exists before doing anything else.
	if _, err := os.Stat(workflowPath); err != nil {
		return fmt.Errorf("conductor: workflow file not found: %s", workflowPath)
	}

	fmt.Printf("conductor: loading workflow from %s\n", workflowPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Build an SDK client using the user's stored configuration.
	cl, err := sdkclient.New(sdkclient.WithClientType(sdkclient.ClientTypeTUI))
	if err != nil {
		return fmt.Errorf("conductor: create client: %w", err)
	}
	defer cl.Stop(ctx)

	// Subscribe to the unified event bus to print conductor lifecycle events.
	cl.Subscribe(func(ev sdkclient.Event) error {
		switch ev.Kind {
		case sdkclient.EventPeerJoined:
			if p, ok := ev.Payload.(sdkclient.PeerJoinedPayload); ok {
				fmt.Printf("[conductor] peer joined: %s\n", p.Handle)
			}
		case sdkclient.EventPeerLeft:
			if p, ok := ev.Payload.(sdkclient.PeerLeftPayload); ok {
				fmt.Printf("[conductor] peer left: %s\n", p.Handle)
			}
		case sdkclient.EventPeerStatus:
			if p, ok := ev.Payload.(sdkclient.PeerStatusPayload); ok {
				fmt.Printf("[conductor] peer %s → %s: %s\n", p.Handle, p.Status, p.CurrentTask)
			}
		case sdkclient.EventTaskDispatched:
			if p, ok := ev.Payload.(sdkclient.TaskDispatchedPayload); ok {
				fmt.Printf("[conductor] dispatched %s → peer %s (attempt %d)\n",
					p.Identifier, p.PeerHandle, p.Attempt)
			}
		case sdkclient.EventTaskCompleted:
			if p, ok := ev.Payload.(sdkclient.TaskCompletedPayload); ok {
				fmt.Printf("[conductor] completed %s (peer %s)\n", p.Identifier, p.PeerHandle)
			}
		case sdkclient.EventTaskFailed:
			if p, ok := ev.Payload.(sdkclient.TaskFailedPayload); ok {
				fmt.Printf("[conductor] FAILED %s after %d attempts: %s\n",
					p.Identifier, p.Attempts, p.Error)
			}
		case sdkclient.EventError:
			if p, ok := ev.Payload.(sdkclient.ErrorPayload); ok {
				fmt.Printf("[conductor] error: %s\n", p.Message)
			}
		}
		return nil
	})

	// Build the Conductor with a LivePeerPool so it can actually send A2A
	// tasks to running TUI/headless peers via HTTP JSON-RPC.
	pool := conductor.NewLivePeerPool("", "")
	cond, err := conductor.New(
		conductor.WithWorkflow(workflowPath),
		conductor.WithClient(cl),
		conductor.WithPool(pool),
	)
	if err != nil {
		return fmt.Errorf("conductor: %w", err)
	}

	if err := cond.Start(ctx); err != nil {
		return fmt.Errorf("conductor: start: %w", err)
	}
	defer cond.Stop()

	fmt.Printf("conductor: started — polling every %dms, watching swarm peers\n",
		cond.Orchestrator().WorkflowConfig().Polling.IntervalMs)
	fmt.Println("conductor: press Ctrl-C to stop")

	// Block until SIGINT or SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nconductor: shutting down…")
	return nil
}
