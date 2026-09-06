package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAICompatibleProviderTranscribeRequestAndVerboseResponse(t *testing.T) {
	pcm := []byte{0x00, 0x80, 0x00, 0x00, 0xff, 0x7f, 0x34, 0x12}
	var handlerErr error

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			handlerErr = fmt.Errorf("method = %s, want POST", r.Method)
			http.Error(w, handlerErr.Error(), http.StatusBadRequest)
			return
		}
		if r.URL.Path != "/v1/audio/transcriptions" {
			handlerErr = fmt.Errorf("path = %s, want /v1/audio/transcriptions", r.URL.Path)
			http.Error(w, handlerErr.Error(), http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			handlerErr = fmt.Errorf("Authorization = %q, want Bearer test-token", got)
			http.Error(w, handlerErr.Error(), http.StatusBadRequest)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			handlerErr = fmt.Errorf("ParseMultipartForm() error = %w", err)
			http.Error(w, handlerErr.Error(), http.StatusBadRequest)
			return
		}
		for field, want := range map[string]string{
			"model":           "nvidia/parakeet-tdt-0.6b-v3",
			"language":        "en",
			"response_format": "verbose_json",
		} {
			if got := r.FormValue(field); got != want {
				handlerErr = fmt.Errorf("field %s = %q, want %q", field, got, want)
				http.Error(w, handlerErr.Error(), http.StatusBadRequest)
				return
			}
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			handlerErr = fmt.Errorf("FormFile(file) error = %w", err)
			http.Error(w, handlerErr.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		if header.Filename != "audio.wav" {
			handlerErr = fmt.Errorf("filename = %q, want audio.wav", header.Filename)
			http.Error(w, handlerErr.Error(), http.StatusBadRequest)
			return
		}
		wav, err := io.ReadAll(file)
		if err != nil {
			handlerErr = fmt.Errorf("read file: %w", err)
			http.Error(w, handlerErr.Error(), http.StatusBadRequest)
			return
		}
		if len(wav) != pcm16WAVHeaderSize+len(pcm) || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
			handlerErr = fmt.Errorf("uploaded file is not a valid canonical WAV header")
			http.Error(w, handlerErr.Error(), http.StatusBadRequest)
			return
		}
		if got := binary.LittleEndian.Uint16(wav[22:24]); got != 1 {
			handlerErr = fmt.Errorf("WAV channels = %d, want 1", got)
			http.Error(w, handlerErr.Error(), http.StatusBadRequest)
			return
		}
		if got := binary.LittleEndian.Uint32(wav[24:28]); got != 16000 {
			handlerErr = fmt.Errorf("WAV sample rate = %d, want 16000", got)
			http.Error(w, handlerErr.Error(), http.StatusBadRequest)
			return
		}
		if !bytes.Equal(wav[44:], pcm) {
			handlerErr = fmt.Errorf("WAV PCM payload = %v, want %v", wav[44:], pcm)
			http.Error(w, handlerErr.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"hello world","language":"en","duration":1.25,"words":[{"word":"hello","start":0.1,"end":0.5,"confidence":0.9},{"word":"world","start":0.6,"end":1.2}]}`)
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(&ProviderConfig{
		BaseURL:    server.URL + "/",
		APIKey:     "test-token",
		Language:   "en",
		SampleRate: 16000,
		Channels:   1,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	result, err := provider.Transcribe(context.Background(), pcm)
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	if result.Text != "hello world" || !result.IsFinal || result.Language != "en" || result.Duration != 1.25 {
		t.Errorf("result = %+v, want parsed verbose response", result)
	}
	if len(result.Words) != 2 {
		t.Fatalf("word count = %d, want 2", len(result.Words))
	}
	if got, want := result.Words[0], (WordTimestamp{Word: "hello", Start: 0.1, End: 0.5, Confidence: 0.9}); got != want {
		t.Errorf("first word = %+v, want %+v", got, want)
	}
	if got, want := result.Words[1], (WordTimestamp{Word: "world", Start: 0.6, End: 1.2}); got != want {
		t.Errorf("second word = %+v, want %+v", got, want)
	}
}

func TestOpenAICompatibleProviderStandardJSONAndNoAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want no header", got)
		}
		_, _ = io.WriteString(w, `{"text":"plain response"}`)
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(&ProviderConfig{BaseURL: server.URL + "/v1"})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}
	result, err := provider.Transcribe(context.Background(), []byte{0, 0})
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if result.Text != "plain response" || !result.IsFinal {
		t.Errorf("result = %+v, want standard JSON text", result)
	}
}

func TestOpenAICompatibleProviderNormalizesBaseURL(t *testing.T) {
	tests := []struct {
		base string
		want string
	}{
		{base: "http://example.test", want: "http://example.test/v1/audio/transcriptions"},
		{base: "http://example.test/", want: "http://example.test/v1/audio/transcriptions"},
		{base: "http://example.test/openai/v1", want: "http://example.test/openai/v1/audio/transcriptions"},
		{base: "http://example.test/v1/audio/transcriptions/", want: "http://example.test/v1/audio/transcriptions"},
	}
	for _, tt := range tests {
		t.Run(tt.base, func(t *testing.T) {
			provider, err := NewOpenAICompatibleProvider(&ProviderConfig{BaseURL: tt.base})
			if err != nil {
				t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
			}
			if provider.endpoint != tt.want {
				t.Errorf("endpoint = %q, want %q", provider.endpoint, tt.want)
			}
		})
	}
}

func TestNewOpenAICompatibleProviderRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ProviderConfig
	}{
		{name: "nil config", cfg: nil},
		{name: "empty URL", cfg: &ProviderConfig{}},
		{name: "relative URL", cfg: &ProviderConfig{BaseURL: "localhost:8001"}},
		{name: "query URL", cfg: &ProviderConfig{BaseURL: "http://localhost:8001?x=1"}},
		{name: "negative sample rate", cfg: &ProviderConfig{BaseURL: "http://localhost:8001", SampleRate: -1}},
		{name: "negative channels", cfg: &ProviderConfig{BaseURL: "http://localhost:8001", Channels: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewOpenAICompatibleProvider(tt.cfg); err == nil {
				t.Fatal("NewOpenAICompatibleProvider() error = nil, want validation error")
			}
		})
	}
}

func TestOpenAICompatibleProviderBoundsErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, strings.Repeat("x", openAICompatibleErrorBodyLimit+1024))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(&ProviderConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}
	_, err = provider.Transcribe(context.Background(), []byte{0, 0})
	if err == nil {
		t.Fatal("Transcribe() error = nil, want HTTP error")
	}
	if !strings.Contains(err.Error(), "502 Bad Gateway") || !strings.Contains(err.Error(), "truncated") {
		t.Errorf("error = %q, want status and truncation marker", err)
	}
	if len(err.Error()) > openAICompatibleErrorBodyLimit+256 {
		t.Errorf("error length = %d, want bounded error response", len(err.Error()))
	}
}

func TestOpenAICompatibleProviderBoundsSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"text":"`+strings.Repeat("x", openAICompatibleSuccessBodyLimit)+`"}`)
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(&ProviderConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Transcribe(context.Background(), []byte{0, 0}); err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("Transcribe() error = %v, want bounded success response error", err)
	}
}

func TestOpenAICompatibleProviderHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	defer server.Close()
	defer close(release)

	provider, err := NewOpenAICompatibleProvider(&ProviderConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := provider.Transcribe(ctx, []byte{0, 0})
		done <- err
	}()
	<-started
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Transcribe() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Transcribe() did not return after context cancellation")
	}
}

func TestOpenAICompatibleProviderHonorsConfiguredTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release)

	provider, err := NewOpenAICompatibleProvider(&ProviderConfig{
		BaseURL:        server.URL,
		RequestTimeout: int64(25 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	started := time.Now()
	_, err = provider.Transcribe(context.Background(), []byte{0, 0})
	if err == nil {
		t.Fatal("Transcribe() error = nil, want timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Transcribe() error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("Transcribe() elapsed = %s, configured timeout was 25ms", elapsed)
	}
}
