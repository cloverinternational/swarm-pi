package chat

import "context"

// Close releases the unified SDK client's background providers, agents, MCP
// connections, subscriptions, and persistent state. It is safe to call when
// startup fell back to degraded mode.
func (sdk *SDKIntegration) Close() error {
	if sdk == nil {
		return nil
	}
	if sdk.planBroker != nil {
		sdk.planBroker.CancelPending(context.Canceled)
	}
	if sdk.sdkClientUnsub != nil {
		sdk.sdkClientUnsub()
		sdk.sdkClientUnsub = nil
	}
	if sdk.sdkClient != nil {
		return sdk.sdkClient.Close()
	}
	return nil
}
