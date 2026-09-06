package voice

import (
	"strings"
	"sync"
)

// TranscriptBuffer manages interim and final transcripts
type TranscriptBuffer struct {
	mu          sync.RWMutex
	finalText   strings.Builder
	interimText string
}

// NewTranscriptBuffer creates a new transcript buffer
func NewTranscriptBuffer() *TranscriptBuffer {
	return &TranscriptBuffer{}
}

// AppendFinal appends final transcript text
func (t *TranscriptBuffer) AppendFinal(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Add space if needed
	if t.finalText.Len() > 0 && !endsWithSpace(t.finalText.String()) {
		t.finalText.WriteString(" ")
	}
	t.finalText.WriteString(text)

	// Clear interim when we get final
	t.interimText = ""
}

// SetInterim sets the interim transcript text
func (t *TranscriptBuffer) SetInterim(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.interimText = text
}

// GetFinalText returns the accumulated final text
func (t *TranscriptBuffer) GetFinalText() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.finalText.String()
}

// GetInterimText returns the current interim text
func (t *TranscriptBuffer) GetInterimText() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.interimText
}

// GetFullText returns the combined final and interim text
func (t *TranscriptBuffer) GetFullText() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := t.finalText.String()
	if t.interimText != "" {
		if result != "" && !endsWithSpace(result) {
			result += " "
		}
		result += t.interimText
	}
	return result
}

// Clear clears all transcript text
func (t *TranscriptBuffer) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.finalText.Reset()
	t.interimText = ""
}

// HasText returns true if there is any transcript text
func (t *TranscriptBuffer) HasText() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.finalText.Len() > 0 || t.interimText != ""
}

// endsWithSpace checks if a string ends with whitespace
func endsWithSpace(s string) bool {
	if len(s) == 0 {
		return false
	}
	last := s[len(s)-1]
	return last == ' ' || last == '\n' || last == '\t'
}

// TranscriptProcessor handles transcript processing and events
type TranscriptProcessor struct {
	buffer    *TranscriptBuffer
	events    chan<- VoiceEvent
	onInterim func(text string)
	onFinal   func(text string)
}

// NewTranscriptProcessor creates a new transcript processor
func NewTranscriptProcessor(events chan<- VoiceEvent) *TranscriptProcessor {
	return &TranscriptProcessor{
		buffer: NewTranscriptBuffer(),
		events: events,
	}
}

// SetOnInterim sets the callback for interim transcripts
func (p *TranscriptProcessor) SetOnInterim(fn func(string)) {
	p.onInterim = fn
}

// SetOnFinal sets the callback for final transcripts
func (p *TranscriptProcessor) SetOnFinal(fn func(string)) {
	p.onFinal = fn
}

// Process processes a transcript message
func (p *TranscriptProcessor) Process(msg TranscriptTextMsg) {
	transcript := Transcript{
		Text:     msg.Text,
		IsFinal:  msg.IsFinal,
		Channel:  msg.Channel,
		Start:    msg.Start,
		Duration: msg.Duration,
	}

	if msg.IsFinal {
		p.buffer.AppendFinal(msg.Text)
		if p.onFinal != nil {
			p.onFinal(msg.Text)
		}
		p.events <- EventFinal{Text: msg.Text}
	} else {
		p.buffer.SetInterim(msg.Text)
		if p.onInterim != nil {
			p.onInterim(msg.Text)
		}
		p.events <- EventInterim{Text: msg.Text}
	}

	p.events <- EventTranscript{Transcript: transcript}
}

// GetFullText returns the current full transcript
func (p *TranscriptProcessor) GetFullText() string {
	return p.buffer.GetFullText()
}

// Clear clears the transcript buffer
func (p *TranscriptProcessor) Clear() {
	p.buffer.Clear()
}
