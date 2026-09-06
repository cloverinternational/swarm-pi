package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// runHeadlessHarness executes one prompt through a client constructed solely
// from the selected harness plan. Provider, model, system prompt, tools,
// workspace, limits, and permission posture come only from the plan. The CLI
// contributes the user message, output format, and explicit D2 yolo gesture.
func runHeadlessHarness(planPath string, allowYolo bool, outputFormat, prompt, conversationID string) error {
	plan, err := harness.Compile(planPath)
	if err != nil {
		return err
	}

	options, err := buildHarnessClientOptions(plan, allowYolo)
	if err != nil {
		return err
	}
	sdk, err := client.New(options...)
	if err != nil {
		return err
	}
	defer func() {
		_ = sdk.Close()
	}()

	snapshot := sdk.HarnessSnapshot()
	headlessLog := newLogWriter(os.Stderr, *verboseFlag, *debugToStderrFlag)

	var stdoutWriter io.Writer = os.Stdout
	if outputFormat == "stream-json" {
		stdoutWriter = newStdoutGuard(os.Stdout, os.Stderr)
	}
	printer := newHeadlessPrinter(
		outputFormat,
		snapshot.Model,
		*verboseFlag,
		*showThinkingFlag,
		false,
		*debugToStderrFlag,
		stdoutWriter,
		headlessLog,
	)
	if printer.isStreamJSON() {
		printer.emitInit(snapshot.ExposedTools)
	}

	ctx := context.Background()
	unsubscribe := sdk.SubscribeUpdates(func(_ context.Context, update agent.IntermediateUpdate) error {
		printer.handleUpdate(update)
		return nil
	})
	defer unsubscribe()

	reply, execErr := sdk.ChatCtx(ctx, conversationID, prompt)
	if printer.resultText == "" {
		printer.resultText = reply
	}
	printer.emitResult(printer.resultText, execErr)
	if execErr != nil {
		return fmt.Errorf("harness execution failed: %w", execErr)
	}
	return nil
}
