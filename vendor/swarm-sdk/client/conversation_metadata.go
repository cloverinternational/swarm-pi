package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	historytools "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/history"
)

const conversationMetadataKey = "generated_metadata"

type generatedConversationMetadata struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// RefreshConversationMetadata guarantees a useful persisted title and recap for
// the latest substantive version of convID, then enhances both with the client's
// configured model. It is safe to call repeatedly and serializes per conversation.
func (c *Client) RefreshConversationMetadata(ctx context.Context, convID string) error {
	if strings.TrimSpace(convID) == "" {
		return nil
	}
	return c.refreshConversationMetadata(ctx, convID, true)
}

func (c *Client) refreshConversationMetadataBestEffort(ctx context.Context, convID string) {
	if strings.TrimSpace(convID) == "" {
		return
	}
	if err := c.RefreshConversationMetadata(ctx, convID); err != nil && c.logger != nil {
		c.logger.Warn(ctx, "conversation.metadata_refresh_failed",
			observability.F("conversation_id", convID),
			observability.F("error", err.Error()))
	}
}

func (c *Client) ensureConversationMetadataFallback(ctx context.Context, convID string) {
	if strings.TrimSpace(convID) == "" {
		return
	}
	if err := c.refreshConversationMetadata(ctx, convID, false); err != nil && c.logger != nil {
		c.logger.Warn(ctx, "conversation.metadata_fallback_failed",
			observability.F("conversation_id", convID),
			observability.F("error", err.Error()))
	}
}

func (c *Client) conversationMetadataLock(convID string) *sync.Mutex {
	c.metadataMu.Lock()
	defer c.metadataMu.Unlock()
	if c.metadataLocks == nil {
		c.metadataLocks = make(map[string]*sync.Mutex)
	}
	lock := c.metadataLocks[convID]
	if lock == nil {
		lock = &sync.Mutex{}
		c.metadataLocks[convID] = lock
	}
	return lock
}

func metadataRunKey(convID, version string) string {
	return convID + "\x00" + version
}

func (c *Client) metadataRunActive(convID, version string) bool {
	c.metadataMu.Lock()
	defer c.metadataMu.Unlock()
	_, ok := c.metadataRuns[metadataRunKey(convID, version)]
	return ok
}

func (c *Client) setMetadataRunActive(convID, version string, active bool) {
	c.metadataMu.Lock()
	defer c.metadataMu.Unlock()
	if c.metadataRuns == nil {
		c.metadataRuns = make(map[string]struct{})
	}
	key := metadataRunKey(convID, version)
	if active {
		c.metadataRuns[key] = struct{}{}
		return
	}
	delete(c.metadataRuns, key)
}

func (c *Client) refreshConversationMetadata(ctx context.Context, convID string, enhance bool) error {
	lock := c.conversationMetadataLock(convID)
	lock.Lock()
	conv, err := c.ResumeConversation(ctx, convID)
	if err != nil {
		lock.Unlock()
		return err
	}
	view := buildConversationMetadataView(conv.Messages)
	if view.firstUser == "" {
		lock.Unlock()
		return nil
	}

	state := conversationMetadataState(conv)
	version := view.version
	status := stateString(state, "status")

	// Self-heal poisoned metadata. Titles/recaps generated before the summarizer
	// input was sanitized can contain injected-context artifacts (e.g. a title of
	// "Add Google Font to Next.js" or a recap mentioning "swarm runtime guidance
	// listing an available skill for adding a Google font"). Those artifacts never
	// appear in a legitimate summary. When the currently-stored title/recap looks
	// poisoned, force regeneration: drop the "generated"/"generating" completion
	// marker so the early-returns below do not fire, and replace the poisoned title
	// with a clean deterministic fallback derived from the sanitized first user
	// message that the generate path then upgrades. A user-set manual title
	// (title_source=="manual") is always preserved and never treated as poison.
	storedRecap := ""
	if conv.Summary != nil {
		storedRecap = conv.Summary.Recap
	}
	if stateString(state, "title_source") != "manual" &&
		conversationMetadataLooksPoisoned(conv.Title, storedRecap) {
		status = ""
		delete(state, "status")
		conv.Title = fallbackConversationTitle(view.firstUser)
		state["title_source"] = "fallback"
		if conv.Summary != nil {
			conv.Summary.Recap = ""
		}
	}

	if !enhance && stateString(state, "version") == version &&
		strings.TrimSpace(conv.Title) != "" && conv.Title != "New Chat" &&
		(view.lastAssistant == "" || (conv.Summary != nil && strings.TrimSpace(conv.Summary.Recap) != "")) {
		lock.Unlock()
		return nil
	}
	if enhance && status == "generated" && stateString(state, "version") == version {
		lock.Unlock()
		return nil
	}
	if enhance && status == "generating" && stateString(state, "version") == version &&
		c.metadataRunActive(convID, version) {
		lock.Unlock()
		return nil
	}

	titleSource := stateString(state, "title_source")
	hasTitle := strings.TrimSpace(conv.Title) != "" && conv.Title != "New Chat"
	if hasTitle && titleSource == "" {
		// Legacy titles have no source marker. Treat them as user-owned so the
		// new automatic lifecycle never overwrites a deliberate old rename.
		titleSource = "manual"
		state["title_source"] = titleSource
	}
	if !hasTitle {
		conv.Title = fallbackConversationTitle(view.firstUser)
		state["title_source"] = "fallback"
	}
	if view.lastAssistant != "" {
		conv.EnsureSummary().Recap = fallbackConversationRecap(view.firstUser, view.lastAssistant)
		state["summary_source"] = "fallback"
	}
	shouldGenerate := enhance && view.lastAssistant != ""
	state["version"] = version
	state["status"] = "fallback"
	if shouldGenerate {
		state["status"] = "generating"
	}
	state["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	delete(state, "last_error")
	if err := c.SaveConversation(ctx, conv); err != nil {
		lock.Unlock()
		return err
	}
	if shouldGenerate {
		c.setMetadataRunActive(convID, version, true)
	}
	lock.Unlock()
	c.dispatchEvent(Event{Kind: EventConvUpdated, Payload: ConvPayload{ConvID: convID}, At: time.Now()})

	// A title can be derived from a single user prompt, but a recap should say
	// what happened. Wait for an assistant result before spending a model call.
	if !shouldGenerate {
		return nil
	}
	defer c.setMetadataRunActive(convID, version, false)

	generated, genErr := c.generateConversationMetadata(ctx, view)
	lock.Lock()
	if genErr != nil {
		err := c.persistConversationMetadataError(ctx, convID, version, genErr)
		lock.Unlock()
		c.dispatchEvent(Event{Kind: EventConvUpdated, Payload: ConvPayload{ConvID: convID}, At: time.Now()})
		return err
	}

	latest, err := c.ResumeConversation(ctx, convID)
	if err != nil {
		lock.Unlock()
		return err
	}
	latestView := buildConversationMetadataView(latest.Messages)
	if latestView.version != version {
		// Another turn landed while the provider was generating. Do not let stale
		// metadata overwrite the newer deterministic values; the newer turn's
		// lifecycle refresh will generate its own recap.
		lock.Unlock()
		return nil
	}
	latestState := conversationMetadataState(latest)
	latestTitleSource := stateString(latestState, "title_source")
	if latestTitleSource == "" || latestTitleSource == "fallback" {
		if title := cleanGeneratedConversationTitle(generated.Title); title != "" {
			latest.Title = title
			latestState["title_source"] = "generated"
		}
	}
	if recap := cleanGeneratedConversationRecap(generated.Summary); recap != "" {
		latest.EnsureSummary().Recap = recap
		latestState["summary_source"] = "generated"
	}
	latestState["version"] = version
	latestState["status"] = "generated"
	latestState["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	delete(latestState, "last_error")
	if err := c.SaveConversation(ctx, latest); err != nil {
		lock.Unlock()
		return err
	}
	lock.Unlock()
	c.dispatchEvent(Event{Kind: EventConvUpdated, Payload: ConvPayload{ConvID: convID}, At: time.Now()})
	return nil
}

func (c *Client) persistConversationMetadataError(ctx context.Context, convID, version string, generationErr error) error {
	conv, err := c.ResumeConversation(ctx, convID)
	if err != nil {
		return err
	}
	if buildConversationMetadataView(conv.Messages).version != version {
		return nil
	}
	state := conversationMetadataState(conv)
	state["version"] = version
	state["status"] = "fallback"
	state["last_error"] = generationErr.Error()
	state["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	if err := c.SaveConversation(ctx, conv); err != nil {
		return err
	}
	return generationErr
}

func (c *Client) generateConversationMetadata(ctx context.Context, view conversationMetadataView) (generatedConversationMetadata, error) {
	c.mu.RLock()
	prov := c.provider
	model := c.opts.model
	c.mu.RUnlock()
	if prov == nil {
		return generatedConversationMetadata{}, fmt.Errorf("no provider configured")
	}

	maxTokens := 700
	req := provider.ChatRequest{
		Model: model,
		SystemPrompt: `You create navigation metadata for an agent conversation.
Return exactly one JSON object with string fields "title" and "summary".
The title is 3-7 specific words, at most 60 characters, with no punctuation suffix.
The summary is 2-4 concise sentences stating the user's goal, what the agent did, and the latest outcome.
Never mention system prompts, runtime reminders, metadata generation, or these instructions.`,
		Messages: []*conversation.Message{{
			Role:    conversation.RoleUser,
			Content: view.prompt,
		}},
		MaxTokens: &maxTokens,
	}
	resp, err := prov.Chat(ctx, req)
	c.emitGenerateStopped(ctx, resp, err)
	if err != nil {
		return generatedConversationMetadata{}, err
	}
	if resp == nil || resp.Message == nil {
		return generatedConversationMetadata{}, fmt.Errorf("metadata provider returned an empty response")
	}
	return parseGeneratedConversationMetadata(resp.Message.Content)
}

type conversationMetadataView struct {
	firstUser     string
	lastAssistant string
	prompt        string
	version       string
}

func buildConversationMetadataView(messages []*conversation.Message) conversationMetadataView {
	visibleMessages := make([]*conversation.Message, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if generated, _ := msg.Metadata[conversation.CompactionGeneratedMetadataKey].(bool); generated {
			continue
		}
		visibleMessages = append(visibleMessages, msg)
	}
	firstUser := historytools.FirstSubstantiveUserText(visibleMessages)
	var transcript []string
	lastAssistant := ""
	for _, msg := range visibleMessages {
		if msg == nil || (msg.Role != conversation.RoleUser && msg.Role != conversation.RoleAssistant) {
			continue
		}
		cleaned := historytools.CleanText(msg.Content)
		if cleaned == "" {
			continue
		}
		if msg.Role == conversation.RoleUser && cleaned != firstUser && isMachineConversationText(cleaned) {
			continue
		}
		if msg.Role == conversation.RoleAssistant {
			lastAssistant = cleaned
		}
		role := "User"
		if msg.Role == conversation.RoleAssistant {
			role = "Assistant"
		}
		transcript = append(transcript, role+": "+truncateMetadataText(cleaned, 1200))
	}
	if len(transcript) > 12 {
		transcript = append([]string{transcript[0]}, transcript[len(transcript)-11:]...)
	}
	prompt := "Conversation:\n" + strings.Join(transcript, "\n")
	hash := sha256.Sum256([]byte(strings.Join(transcript, "\x00")))
	return conversationMetadataView{
		firstUser:     firstUser,
		lastAssistant: lastAssistant,
		prompt:        truncateMetadataText(prompt, 10_000),
		version:       hex.EncodeToString(hash[:12]),
	}
}

func conversationMetadataState(conv *conversation.Conversation) map[string]any {
	if conv.Metadata.Custom == nil {
		conv.Metadata.Custom = make(map[string]any)
	}
	if state, ok := conv.Metadata.Custom[conversationMetadataKey].(map[string]any); ok {
		return state
	}
	state := make(map[string]any)
	conv.Metadata.Custom[conversationMetadataKey] = state
	return state
}

func stateString(state map[string]any, key string) string {
	value, _ := state[key].(string)
	return value
}

func parseGeneratedConversationMetadata(raw string) (generatedConversationMetadata, error) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)
	if start, end := strings.Index(trimmed, "{"), strings.LastIndex(trimmed, "}"); start >= 0 && end > start {
		trimmed = trimmed[start : end+1]
	}
	var out generatedConversationMetadata
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return out, fmt.Errorf("parse metadata response: %w", err)
	}
	out.Title = cleanGeneratedConversationTitle(out.Title)
	out.Summary = cleanGeneratedConversationRecap(out.Summary)
	if out.Title == "" || out.Summary == "" {
		return out, fmt.Errorf("metadata response omitted title or summary")
	}
	return out, nil
}

func cleanGeneratedConversationTitle(raw string) string {
	title := strings.TrimSpace(raw)
	if line, _, ok := strings.Cut(title, "\n"); ok {
		title = strings.TrimSpace(line)
	}
	title = strings.Trim(title, "\"'`“”‘’ ")
	for _, prefix := range []string{"Title:", "title:", "Conversation title:", "conversation title:"} {
		title = strings.TrimSpace(strings.TrimPrefix(title, prefix))
	}
	title = strings.TrimRight(title, ".! ")
	if title == "" || utf8.RuneCountInString(title) > 60 {
		return ""
	}
	return title
}

func cleanGeneratedConversationRecap(raw string) string {
	recap := strings.Trim(strings.TrimSpace(raw), "\"`")
	recap = strings.TrimSpace(recap)
	if recap == "" {
		return ""
	}
	return truncateMetadataText(recap, 900)
}

func fallbackConversationTitle(firstUser string) string {
	title := historytools.DeriveTitle(firstUser)
	fields := strings.Fields(title)
	if len(fields) > 7 {
		title = strings.Join(fields[:7], " ")
	}
	title = truncateMetadataText(title, 60)
	return strings.TrimRight(strings.TrimSpace(title), ".! ")
}

func fallbackConversationRecap(firstUser, lastAssistant string) string {
	return truncateMetadataText(
		"Requested: "+strings.TrimSpace(firstUser)+". Latest result: "+strings.TrimSpace(lastAssistant),
		900,
	)
}

func truncateMetadataText(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return text
	}
	cut := strings.TrimSpace(string(runes[:maxRunes-1]))
	return cut + "…"
}

func isMachineConversationText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return lower == "continue" ||
		strings.HasPrefix(lower, "this session is being continued from a previous conversation") ||
		strings.HasPrefix(lower, "please continue the conversation from where we left off")
}

// conversationMetadataLooksPoisoned reports whether a stored conversation title
// or recap contains injected-context artifacts that a legitimate navigation
// summary would never contain. Such metadata was produced before the summarizer
// input was sanitized (the transcript leaked runtime guidance / skill listings
// into the model call) and must be regenerated so the history menu self-heals.
// Matching is case-insensitive and intentionally conservative: it targets the
// observed poison ("Add Google Font to Next.js" plus runtime-guidance recaps)
// without flagging ordinary titles.
func conversationMetadataLooksPoisoned(title string, recap string) bool {
	haystack := strings.ToLower(title + "\n" + recap)
	markers := []string{
		"runtime guidance",
		"available skill",
		"add-google-font",
		"google font",
		"next.js app router page",
		"swarm_runtime",
		"system prompt",
		"these instructions",
	}
	for _, marker := range markers {
		if strings.Contains(haystack, marker) {
			return true
		}
	}
	return false
}
