package chat

// ============================================================================
// Message types for swarm hub chat (WorkspaceHub peer-to-peer messaging)
// ============================================================================

// swarmChatInboundMsg is fired when a hub peer sends a chat message.
// The message is appended inline to a.messages as a system entry.
type swarmChatInboundMsg struct {
	From    string
	Content string
}

// swarmChatPeerJoinedMsg is fired when a new peer connects to the hub.
type swarmChatPeerJoinedMsg struct {
	Handle string
}

// swarmChatPeerLeftMsg is fired when a peer disconnects from the hub.
type swarmChatPeerLeftMsg struct {
	Handle string
}
