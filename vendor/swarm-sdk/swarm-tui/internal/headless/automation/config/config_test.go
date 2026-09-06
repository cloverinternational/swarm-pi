package config_test

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/config"
)

var testYAML = `
screens:
  home:
    id: "home"
    type: "ScreenHome"
    regions:
      - id: "new_chat_button"
        bounds: { x: 2, y: 5, width: 20, height: 3 }
        clickable: true
        navigation_target: "chat"
    navigation:
      to_chat: ["n"]
      to_settings: ["s"]
    detection:
      text_patterns:
        - "New Chat"

  chat:
    id: "chat"
    type: "ScreenChat"
    regions:
      - id: "message_list"
        bounds: { x: 0, y: 2, width: -38, height: -6 }
        scrollable: true
      - id: "input_box"
        bounds: { x: 2, y: -3, width: -40, height: 3 }
        input: true
    navigation:
      to_home: ["esc"]
    detection:
      text_patterns:
        - "│"
`

func TestParseConfig(t *testing.T) {
	cfg, err := config.Parse([]byte(testYAML))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(cfg.Screens) != 2 {
		t.Errorf("Expected 2 screens, got %d", len(cfg.Screens))
	}

	home := cfg.GetScreen("home")
	if home == nil {
		t.Fatal("Home screen not found")
	}

	if home.Type != "ScreenHome" {
		t.Errorf("Expected type ScreenHome, got %s", home.Type)
	}

	if len(home.Regions) != 1 {
		t.Errorf("Expected 1 region, got %d", len(home.Regions))
	}

	if home.Regions[0].ID != "new_chat_button" {
		t.Errorf("Expected region ID 'new_chat_button', got '%s'", home.Regions[0].ID)
	}
}

func TestNavigationPath(t *testing.T) {
	cfg, err := config.Parse([]byte(testYAML))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Home to chat
	keys, err := cfg.GetNavigationPath("home", "chat")
	if err != nil {
		t.Fatalf("GetNavigationPath failed: %v", err)
	}

	if len(keys) != 1 || keys[0] != "n" {
		t.Errorf("Expected ['n'], got %v", keys)
	}

	// Chat to home
	keys, err = cfg.GetNavigationPath("chat", "home")
	if err != nil {
		t.Fatalf("GetNavigationPath failed: %v", err)
	}

	if len(keys) != 1 || keys[0] != "esc" {
		t.Errorf("Expected ['esc'], got %v", keys)
	}
}

func TestBoundsResolve(t *testing.T) {
	// Positive bounds (unchanged)
	b := config.Bounds{X: 10, Y: 5, Width: 20, Height: 10}
	resolved := b.Resolve(100, 50)
	if resolved.X != 10 || resolved.Y != 5 || resolved.Width != 20 || resolved.Height != 10 {
		t.Errorf("Positive bounds should be unchanged: %+v", resolved)
	}

	// Negative X (from right)
	b = config.Bounds{X: -30, Y: 0, Width: 30, Height: 10}
	resolved = b.Resolve(100, 50)
	if resolved.X != 70 {
		t.Errorf("Expected X=70, got %d", resolved.X)
	}

	// Negative Y (from bottom)
	b = config.Bounds{X: 0, Y: -10, Width: 20, Height: 10}
	resolved = b.Resolve(100, 50)
	if resolved.Y != 40 {
		t.Errorf("Expected Y=40, got %d", resolved.Y)
	}

	// Negative width (extend to edge)
	b = config.Bounds{X: 0, Y: 0, Width: -20, Height: 10}
	resolved = b.Resolve(100, 50)
	if resolved.Width != 80 {
		t.Errorf("Expected Width=80, got %d", resolved.Width)
	}

	// Negative height (extend to edge)
	b = config.Bounds{X: 0, Y: 0, Width: 20, Height: -10}
	resolved = b.Resolve(100, 50)
	if resolved.Height != 40 {
		t.Errorf("Expected Height=40, got %d", resolved.Height)
	}
}

func TestNavigator(t *testing.T) {
	cfg, err := config.Parse([]byte(testYAML))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	nav := config.NewNavigator(cfg)

	// Set current screen
	nav.SetCurrentScreen("home")
	if nav.GetCurrentScreen() != "home" {
		t.Errorf("Expected current screen 'home', got '%s'", nav.GetCurrentScreen())
	}

	// Get keys to chat
	keys, err := nav.GetKeysTo("chat")
	if err != nil {
		t.Fatalf("GetKeysTo failed: %v", err)
	}

	if len(keys) != 1 || keys[0] != "n" {
		t.Errorf("Expected ['n'], got %v", keys)
	}
}

func TestDetectScreen(t *testing.T) {
	cfg, err := config.Parse([]byte(testYAML))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	nav := config.NewNavigator(cfg)

	// Detect home screen
	content := "Welcome!\nNew Chat\nSettings"
	detected := nav.DetectScreen(content)
	if detected != "home" {
		t.Errorf("Expected 'home', got '%s'", detected)
	}

	// Detect chat screen
	content = "│ Hello\n│ World"
	detected = nav.DetectScreen(content)
	if detected != "chat" {
		t.Errorf("Expected 'chat', got '%s'", detected)
	}
}

func TestFindRegion(t *testing.T) {
	cfg, err := config.Parse([]byte(testYAML))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Find region in home screen
	screen, region := cfg.FindRegion("new_chat_button")
	if screen == nil || region == nil {
		t.Fatal("Region not found")
	}

	if screen.ID != "home" {
		t.Errorf("Expected screen 'home', got '%s'", screen.ID)
	}

	if !region.Clickable {
		t.Error("Expected region to be clickable")
	}

	// Find region in chat screen
	screen, region = cfg.FindRegion("input_box")
	if screen == nil || region == nil {
		t.Fatal("Region not found")
	}

	if screen.ID != "chat" {
		t.Errorf("Expected screen 'chat', got '%s'", screen.ID)
	}

	if !region.Input {
		t.Error("Expected region to be input")
	}

	// Non-existent region
	screen, region = cfg.FindRegion("nonexistent")
	if screen != nil || region != nil {
		t.Error("Expected nil for non-existent region")
	}
}
