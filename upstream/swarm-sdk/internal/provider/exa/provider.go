package exa

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// Provider implements provider.Provider using the Exa search API.
//
// This is a *search-only* provider — it does not generate text. The Chat()
// method extracts the last user message, uses it as the Exa query, and
// returns formatted search results as the assistant response.
//
// Stream() wraps Chat() into a single-chunk channel.
type Provider struct {
	cfg    *Config
	client *apiClient
}

var _ provider.Provider = &Provider{}

// New creates a new Exa provider from a provider.Config.
// The API key is read from cfg.APIKey; if empty, falls back to EXA_API_KEY env var.
func New(cfg provider.Config) (*Provider, error) {
	exaCfg := DefaultConfig()

	if cfg.APIKey != "" {
		exaCfg.APIKey = cfg.APIKey
	}
	if cfg.BaseURL != "" {
		exaCfg.BaseURL = cfg.BaseURL
	}

	// Read search-specific options from the generic Custom map.
	if cfg.Custom != nil {
		if v, ok := cfg.Custom["search_type"].(string); ok && v != "" {
			exaCfg.SearchType = v
		}
		if v, ok := cfg.Custom["use_answer"].(bool); ok {
			exaCfg.UseAnswer = v
		}
		if v, ok := cfg.Custom["num_results"].(int); ok && v > 0 {
			exaCfg.NumResults = v
		}
	}

	if err := exaCfg.validate(); err != nil {
		return nil, err
	}

	return &Provider{
		cfg:    &exaCfg,
		client: newAPIClient(&exaCfg),
	}, nil
}

// NewDirect creates a Provider directly from an Exa-specific Config.
func NewDirect(cfg Config) (*Provider, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Provider{
		cfg:    &cfg,
		client: newAPIClient(&cfg),
	}, nil
}

// Name returns "exa".
func (p *Provider) Name() string { return "exa" }

// Chat treats the last user message as a search query and returns formatted results.
//
// The ChatRequest.Model field is not used (Exa is not an LLM). The system
// prompt and temperature fields are silently ignored.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	query := extractQuery(req)
	if query == "" {
		return nil, fmt.Errorf("exa: no user message found in request — cannot determine search query")
	}

	startTime := time.Now()

	var content string
	if p.cfg.UseAnswer {
		resp, err := p.client.Answer(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("exa answer: %w", err)
		}
		content = formatAnswerResponse(query, resp, time.Since(startTime))
	} else {
		resp, err := p.client.Search(ctx, query, SearchOpts{
			NumResults: p.cfg.NumResults,
			SearchType: p.cfg.SearchType,
			WithText:   true,
		})
		if err != nil {
			return nil, fmt.Errorf("exa search: %w", err)
		}
		content = formatSearchResponse(query, resp, time.Since(startTime))
	}

	msg := &conversation.Message{
		Role:      conversation.RoleAssistant,
		Content:   content,
		Timestamp: time.Now(),
		Provider:  "exa",
		Model:     "exa-search",
	}

	return &provider.ChatResponse{
		Message:      msg,
		FinishReason: provider.FinishReasonStop,
	}, nil
}

// Stream wraps Chat() into a single-chunk channel.
// Exa does not have a native streaming API.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		defer close(ch)
		resp, err := p.Chat(ctx, req)
		if err != nil {
			ch <- provider.StreamChunk{Error: err, Done: true}
			return
		}
		content := ""
		if resp.Message != nil {
			content = resp.Message.Content
		}
		var orderedBlocks []conversation.MessageBlock
		if content != "" {
			orderedBlocks = []conversation.MessageBlock{{
				Type:     conversation.BlockTypeContent,
				Content:  content,
				Sequence: 0,
			}}
		}
		ch <- provider.StreamChunk{
			Delta:         content,
			Done:          true,
			FinishReason:  provider.FinishReasonStop,
			OrderedBlocks: orderedBlocks,
		}
	}()
	return ch, nil
}

// Capabilities describes Exa as a search-only provider.
func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:            false, // single-response only
		FunctionCalling:      false,
		Vision:               false,
		MaxContextWindow:     0,
		MaxOutputTokens:      0,
		SupportsSystemPrompt: false,
		SupportsTemperature:  false,
		PromptCaching:        false,
		SupportsJSON:         false,
		SupportedModels:      []string{"exa-search", "exa-answer"},
	}
}

// ─── Formatting helpers ───────────────────────────────────────────────────────

func formatSearchResponse(query string, resp *SearchResponse, dur time.Duration) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Web Search Results for: %q\n", query)
	sb.WriteString("================================================\n\n")

	if len(resp.Results) == 0 {
		sb.WriteString("No results found.\n")
	}
	for i, r := range resp.Results {
		fmt.Fprintf(&sb, "## %d. %s\n", i+1, r.Title)
		fmt.Fprintf(&sb, "**URL:** %s\n", r.URL)
		if r.PublishedDate != "" {
			fmt.Fprintf(&sb, "**Published:** %s\n", r.PublishedDate)
		}
		if r.Author != "" {
			fmt.Fprintf(&sb, "**Author:** %s\n", r.Author)
		}
		if r.Score > 0 {
			fmt.Fprintf(&sb, "**Relevance:** %.2f\n", r.Score)
		}
		if r.Summary != "" {
			fmt.Fprintf(&sb, "\n%s\n\n", r.Summary)
		} else if r.Text != "" {
			preview := r.Text
			if len(preview) > 500 {
				preview = preview[:500] + "…"
			}
			fmt.Fprintf(&sb, "\n%s\n\n", preview)
		}
	}

	sb.WriteString("================================================\n")
	fmt.Fprintf(&sb, "Backend: Exa (api.exa.ai) | Duration: %dms | Results: %d\n",
		dur.Milliseconds(), len(resp.Results))
	sb.WriteString("\nREMINDER: Include relevant source URLs in your response to the user.")
	return sb.String()
}

func formatAnswerResponse(query string, resp *AnswerResponse, dur time.Duration) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Answer for: %q\n", query)
	sb.WriteString("================================================\n\n")
	sb.WriteString(resp.Answer)
	sb.WriteString("\n\n")

	if len(resp.Citations) > 0 {
		sb.WriteString("### Sources\n\n")
		for i, c := range resp.Citations {
			fmt.Fprintf(&sb, "%d. [%s](%s)", i+1, c.Title, c.URL)
			if c.PublishedDate != "" {
				fmt.Fprintf(&sb, " — %s", c.PublishedDate)
			}
			sb.WriteByte('\n')
		}
	}

	sb.WriteString("\n================================================\n")
	fmt.Fprintf(&sb, "Backend: Exa /answer | Duration: %dms\n", dur.Milliseconds())
	return sb.String()
}

// extractQuery returns the content of the last user message in the request.
func extractQuery(req provider.ChatRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role == "user" && strings.TrimSpace(msg.Content) != "" {
			return strings.TrimSpace(msg.Content)
		}
	}
	return ""
}
