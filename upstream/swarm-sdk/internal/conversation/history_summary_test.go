package conversation

import (
	"encoding/json"
	"fmt"
	"testing"
)

var historySummarySink MessageHistorySummary

func TestSummarizeMessages(t *testing.T) {
	messages := []*Message{
		{ID: "one", Role: RoleUser, ToolResults: []ToolResult{{CallID: "call-1"}}},
		nil,
		{ID: "two", Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call-1"}, {ID: "call-2"}}},
		{ID: "three", Role: RolePeer},
		{ID: "four", Role: Role("future")},
	}

	got := SummarizeMessages(messages)
	if got.MessageCount != 5 || got.NilCount != 1 {
		t.Fatalf("counts = messages:%d nil:%d, want 5 and 1", got.MessageCount, got.NilCount)
	}
	if got.Roles.User != 1 || got.Roles.Assistant != 1 || got.Roles.Peer != 1 || got.Roles.Other != 1 {
		t.Fatalf("role histogram = %+v", got.Roles)
	}
	if got.ToolCalls != 2 || got.ToolResults != 1 {
		t.Fatalf("tool totals = calls:%d results:%d, want 2 and 1", got.ToolCalls, got.ToolResults)
	}
	if len(got.IDSequenceHash) != 16 {
		t.Fatalf("hash length = %d, want 16", len(got.IDSequenceHash))
	}

	same := SummarizeMessages(messages)
	if same.IDSequenceHash != got.IDSequenceHash {
		t.Fatal("same ID sequence produced a different hash")
	}
	reordered := append([]*Message(nil), messages...)
	reordered[0], reordered[2] = reordered[2], reordered[0]
	if SummarizeMessages(reordered).IDSequenceHash == got.IDSequenceHash {
		t.Fatal("reordered IDs produced the same hash")
	}
}

func TestSummarizeMessagesHashIsUnambiguous(t *testing.T) {
	left := SummarizeMessages([]*Message{{ID: "ab"}, {ID: "c"}})
	right := SummarizeMessages([]*Message{{ID: "a"}, {ID: "bc"}})
	if left.IDSequenceHash == right.IDSequenceHash {
		t.Fatal("length-delimited ID sequences collided")
	}
}

func TestSummarizeMessagesOutputIsBounded(t *testing.T) {
	makeMessages := func(count int) []*Message {
		messages := make([]*Message, count)
		for i := range messages {
			messages[i] = &Message{
				ID:   fmt.Sprintf("message-%08d-with-an-intentionally-long-identifier", i),
				Role: RoleUser,
				ToolCalls: []ToolCall{
					{ID: fmt.Sprintf("tool-%d", i)},
				},
			}
		}
		return messages
	}

	smallJSON, err := json.Marshal(SummarizeMessages(makeMessages(1)))
	if err != nil {
		t.Fatal(err)
	}
	largeJSON, err := json.Marshal(SummarizeMessages(makeMessages(10_000)))
	if err != nil {
		t.Fatal(err)
	}
	if len(largeJSON) > 512 {
		t.Fatalf("10k-message summary is %d bytes, want <= 512", len(largeJSON))
	}
	if len(largeJSON)-len(smallJSON) > 32 {
		t.Fatalf("summary grew by %d bytes with history size, want <= 32", len(largeJSON)-len(smallJSON))
	}

	smallMessages := makeMessages(1)
	largeMessages := makeMessages(10_000)
	smallAllocs := testing.AllocsPerRun(10, func() {
		historySummarySink = SummarizeMessages(smallMessages)
	})
	largeAllocs := testing.AllocsPerRun(10, func() {
		historySummarySink = SummarizeMessages(largeMessages)
	})
	if smallAllocs != largeAllocs || largeAllocs > 1 {
		t.Fatalf("summary allocations grew with history: one=%g 10k=%g, want equal and <= 1", smallAllocs, largeAllocs)
	}
}

func BenchmarkSummarizeMessages(b *testing.B) {
	for _, count := range []int{1, 100, 10_000} {
		messages := make([]*Message, count)
		for i := range messages {
			messages[i] = &Message{ID: fmt.Sprintf("message-%d", i), Role: RoleUser}
		}
		b.Run(fmt.Sprintf("messages_%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_ = SummarizeMessages(messages)
			}
		})
	}
}
