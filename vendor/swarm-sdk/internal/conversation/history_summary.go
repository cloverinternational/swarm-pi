package conversation

import (
	"encoding/hex"
)

// RoleHistogram is a bounded count of canonical message roles.
// Other includes empty and forward-compatible roles not known by this SDK.
type RoleHistogram struct {
	User      int `json:"user"`
	Assistant int `json:"assistant"`
	System    int `json:"system"`
	Tool      int `json:"tool"`
	Peer      int `json:"peer"`
	Other     int `json:"other"`
}

// MessageHistorySummary is a bounded, content-free description of a message
// sequence suitable for default structured logging.
type MessageHistorySummary struct {
	MessageCount   int           `json:"message_count"`
	NilCount       int           `json:"nil_count"`
	Roles          RoleHistogram `json:"roles"`
	ToolCalls      int           `json:"tool_calls"`
	ToolResults    int           `json:"tool_results"`
	IDSequenceHash string        `json:"id_sequence_hash"`
}

// SummarizeMessages returns a deterministic, bounded summary of messages.
// The hash covers the ordered, length-delimited message IDs, including nil
// entries, without retaining IDs or message content in the result.
func SummarizeMessages(messages []*Message) MessageHistorySummary {
	summary := MessageHistorySummary{MessageCount: len(messages)}
	sequenceHash := uint64(14695981039346656037)

	for _, message := range messages {
		if message == nil {
			summary.NilCount++
			sequenceHash = hashID(sequenceHash, "", true)
			continue
		}

		sequenceHash = hashID(sequenceHash, message.ID, false)
		summary.ToolCalls += len(message.ToolCalls)
		summary.ToolResults += len(message.ToolResults)
		switch message.Role {
		case RoleUser:
			summary.Roles.User++
		case RoleAssistant:
			summary.Roles.Assistant++
		case RoleSystem:
			summary.Roles.System++
		case RoleTool:
			summary.Roles.Tool++
		case RolePeer:
			summary.Roles.Peer++
		default:
			summary.Roles.Other++
		}
	}

	var hashBytes [8]byte
	for i := range hashBytes {
		hashBytes[len(hashBytes)-1-i] = byte(sequenceHash >> (i * 8))
	}
	var encoded [16]byte
	hex.Encode(encoded[:], hashBytes[:])
	summary.IDSequenceHash = string(encoded[:])
	return summary
}

func hashID(current uint64, id string, nilMessage bool) uint64 {
	// Reserve MaxUint64 for nil so nil and an empty ID remain distinct.
	length := uint64(len(id))
	if nilMessage {
		length = ^uint64(0)
	}
	for shift := 56; shift >= 0; shift -= 8 {
		current ^= uint64(byte(length >> shift))
		current *= 1099511628211
	}
	for i := range len(id) {
		current ^= uint64(id[i])
		current *= 1099511628211
	}
	return current
}
