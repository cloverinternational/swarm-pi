// Command voice-probe submits raw PCM16LE audio through the production
// OpenAI-compatible voice provider and prints the normalized result as JSON.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/voice"
)

func main() {
	endpoint := flag.String("endpoint", voice.DefaultLocalTranscriptionURL, "OpenAI-compatible transcription server")
	model := flag.String("model", voice.DefaultLocalTranscriptionModel, "transcription model")
	language := flag.String("language", voice.DefaultLanguage, "audio language")
	file := flag.String("pcm", "", "raw signed 16-bit little-endian PCM file")
	rate := flag.Int("rate", voice.DefaultSampleRate, "PCM sample rate")
	channels := flag.Int("channels", voice.DefaultChannels, "PCM channels")
	timeout := flag.Duration("timeout", 2*time.Minute, "request timeout")
	flag.Parse()
	if *file == "" {
		fmt.Fprintln(os.Stderr, "-pcm is required")
		os.Exit(2)
	}
	pcm, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read PCM: %v\n", err)
		os.Exit(1)
	}
	provider, err := voice.NewOpenAICompatibleProvider(&voice.ProviderConfig{
		Type:           voice.ProviderOpenAICompatible,
		BaseURL:        *endpoint,
		Model:          *model,
		Language:       *language,
		SampleRate:     *rate,
		Channels:       *channels,
		RequestTimeout: int64(*timeout),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create provider: %v\n", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	result, err := provider.Transcribe(ctx, pcm)
	if err != nil {
		fmt.Fprintf(os.Stderr, "transcribe: %v\n", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "encode result: %v\n", err)
		os.Exit(1)
	}
}
