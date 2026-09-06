// task-reminder-daemon — local cron daemon that checks Linear and speaks
// a reminder about open tasks using Google TTS → ffplay.
//
// Usage:
//
//	go run ./cmd/task-reminder-daemon -interval 30s -team SWA
//
// It fires immediately on start, then on every tick. Ctrl-C for clean shutdown.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
)

func main() {
	interval := flag.Duration("interval", 2*time.Minute, "how often to check Linear and remind")
	team := flag.String("team", "SWA", "Linear team key to check")
	model := flag.String("model", "claude-sonnet-4-5", "LLM model to use")
	flag.Parse()

	log.Printf("task-reminder-daemon starting — checking %s every %s", *team, *interval)

	// SDK client — let auto-config find credentials from ~/.swarm/tui_accounts.json
	c, err := client.New(
		client.WithProvider(client.ProviderAnthropic, *model),
	)
	if err != nil {
		log.Fatalf("client.New: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := c.Start(ctx); err != nil {
		log.Fatalf("client.Start: %v", err)
	}
	defer func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		_ = c.Stop(shutCtx)
		_ = c.Close()
	}()

	// Guard against overlapping ticks (agent turns can be slow)
	var running sync.Mutex

	fire := func() {
		if !running.TryLock() {
			log.Println("skipping tick — previous turn still running")
			return
		}
		defer running.Unlock()
		checkAndRemind(ctx, c, *team)
	}

	// Fire immediately, then on every tick
	log.Println("firing first check now...")
	fire()

	tick := time.NewTicker(*interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("shutdown signal — stopping daemon")
			return
		case <-tick.C:
			log.Printf("[tick] %s — checking Linear...", time.Now().Format("15:04:05"))
			go fire()
		}
	}
}

// checkAndRemind runs clinear, asks the agent for a summary, then speaks it.
func checkAndRemind(ctx context.Context, c *client.Client, team string) {
	// Step 1: fetch Linear issues via clinear
	log.Printf("running: NO_COLOR=1 clinear issue list --team %s --limit 20", team)
	cmd := exec.CommandContext(ctx, "clinear", "issue", "list", "--team", team, "--limit", "20")
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	out, err := cmd.Output()
	if err != nil {
		log.Printf("clinear failed: %v", err)
		return
	}

	// Step 2: strip ANSI escape codes from clinear output
	issues := stripANSI(string(out))
	log.Printf("got %d bytes of issue data", len(issues))

	// Step 3: ask the agent for a brief, speakable reminder
	prompt := fmt.Sprintf(`Here is a snapshot of the current open Linear issues:

%s

Based on this, give me a short spoken reminder (2-3 sentences max, no markdown, plain English)
that tells me the most important tasks I need to finish today. Mention specific task IDs.
Start with "Hey, you need to finish" — make it feel urgent but brief.`, issues)

	turnCtx, turnCancel := context.WithTimeout(ctx, 90*time.Second)
	defer turnCancel()

	log.Println("asking agent to summarize tasks...")
	reply, err := c.ChatCtx(turnCtx, "", prompt)
	if err != nil {
		log.Printf("agent turn error: %v", err)
		return
	}

	// Clean up reply for TTS (remove any markdown that slipped through)
	reply = cleanForSpeech(reply)

	log.Printf("\n\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n"+
		"  REMINDER: %s\n"+
		"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n", reply)

	// Step 4: speak it
	speak(reply)
}

// speak outputs the text via Google TTS → ffplay, falling back to terminal bell + stdout.
func speak(text string) {
	// Truncate for TTS (Google TTS has ~200 char limit per request)
	ttsText := text
	if len(ttsText) > 190 {
		// Find last space before 190 to avoid cutting mid-word
		cut := strings.LastIndex(ttsText[:190], " ")
		if cut > 0 {
			ttsText = ttsText[:cut]
		} else {
			ttsText = ttsText[:190]
		}
	}

	tmpFile := "/tmp/swarm_tts_reminder.mp3"

	// Build Google Translate TTS URL
	ttsURL := "https://translate.google.com/translate_tts?ie=UTF-8&q=" +
		url.QueryEscape(ttsText) +
		"&tl=en&client=tw-ob"

	// Download with curl (Google TTS requires a browser-like User-Agent)
	dlCtx, dlCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dlCancel()
	curlCmd := exec.CommandContext(dlCtx, "curl", "-s", "-L",
		"-H", "User-Agent: Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
		"-H", "Referer: https://translate.google.com/",
		"-o", tmpFile,
		ttsURL)
	if err := curlCmd.Run(); err != nil {
		log.Printf("TTS download failed: %v — falling back to terminal output", err)
		bellFallback(text)
		return
	}

	// Verify we got an actual MP3 (not an HTML error page)
	if fi, err := os.Stat(tmpFile); err != nil || fi.Size() < 100 {
		log.Printf("TTS file too small (%v) — using terminal fallback", fi)
		bellFallback(text)
		return
	}

	// Play via ffplay (silent mode, auto-exit when done)
	playCtx, playCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer playCancel()
	playCmd := exec.CommandContext(playCtx, "ffplay",
		"-nodisp", "-autoexit", "-loglevel", "quiet", tmpFile)
	if err := playCmd.Run(); err != nil {
		log.Printf("ffplay failed: %v — using terminal fallback", err)
		bellFallback(text)
	}
	log.Println("TTS playback complete")
}

// bellFallback rings the terminal bell and prints a big banner.
func bellFallback(text string) {
	// Ring bell 3 times
	fmt.Print("\a\a\a")
	time.Sleep(200 * time.Millisecond)
	width := 60
	border := strings.Repeat("█", width)
	fmt.Printf("\n%s\n", border)
	fmt.Printf("█  %-*s  █\n", width-5, "🔔 SWARM REMINDER")
	fmt.Printf("█  %-*s  █\n", width-5, "")
	// Word-wrap the text
	words := strings.Fields(text)
	line := ""
	for _, w := range words {
		if len(line)+len(w)+1 > width-6 {
			fmt.Printf("█  %-*s  █\n", width-5, line)
			line = w
		} else {
			if line != "" {
				line += " "
			}
			line += w
		}
	}
	if line != "" {
		fmt.Printf("█  %-*s  █\n", width-5, line)
	}
	fmt.Printf("█  %-*s  █\n", width-5, "")
	fmt.Printf("%s\n\n", border)
}

// stripANSI removes ANSI escape sequences from terminal output.
func stripANSI(s string) string {
	// Simple state machine: skip everything between ESC[ and the next letter
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// Skip until we hit a letter (the command character)
			i += 2
			for i < len(s) && (s[i] < 'A' || s[i] > 'z') {
				i++
			}
			i++ // skip the command char
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

// cleanForSpeech removes markdown artifacts that would sound bad when spoken.
func cleanForSpeech(s string) string {
	replacer := strings.NewReplacer(
		"**", "",
		"*", "",
		"_", "",
		"`", "",
		"#", "",
		"- ", "",
		"\n\n", ". ",
		"\n", " ",
	)
	result := replacer.Replace(s)
	// Collapse multiple spaces
	for strings.Contains(result, "  ") {
		result = strings.ReplaceAll(result, "  ", " ")
	}
	return strings.TrimSpace(result)
}

// verifyTTSEndpoint does a quick HEAD check that Google TTS is reachable.
func verifyTTSEndpoint() bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Head("https://translate.google.com")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

func init() {
	if !verifyTTSEndpoint() {
		log.Println("note: Google TTS endpoint unreachable — will use terminal bell fallback")
	}
}
