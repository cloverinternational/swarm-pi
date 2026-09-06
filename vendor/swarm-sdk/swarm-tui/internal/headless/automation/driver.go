// Package automation provides a headless TUI automation system.
//
// The automation package enables running Bubbletea TUI applications
// without a real terminal, similar to how Playwright works for browsers.
//
// Key features:
//   - Run TUI headlessly without a terminal
//   - Send keys, mouse events, and resize commands
//   - Capture and inspect rendered frames
//   - Navigate using screen configurations
//   - Query state using CSS-like selectors
//
// Basic usage:
//
//	driver := automation.NewDriver(myModel,
//	    automation.WithSize(80, 24),
//	)
//	driver.Start()
//	defer driver.Stop()
//
//	driver.SendKey("down")
//	frame := driver.GetFrame()
//	text := frame.GetText(0, 0, 80, 24)
package automation

import (
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/capture"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/config"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/inject"
	"golang.org/x/text/unicode/norm"
)

// Driver is the main automation controller for headless TUI testing.
type Driver struct {
	mu sync.RWMutex

	// Model
	model   tea.Model
	started bool

	// Dimensions
	width  int
	height int

	// Components
	injector  *inject.Injector
	buffer    *capture.FrameBuffer
	navigator *config.Navigator

	// Configuration
	traceEnabled bool
	configPath   string
	screenConfig *config.Config

	// Internal state
	updateCount int
}

// NewDriver creates a new automation driver for the given model.
func NewDriver(model tea.Model, opts ...Option) *Driver {
	d := &Driver{
		model:  model,
		width:  80,
		height: 24,
	}

	for _, opt := range opts {
		opt(d)
	}

	d.injector = inject.NewInjector(d.width, d.height)
	d.buffer = capture.NewFrameBuffer(d.width, d.height)

	return d
}

// Option configures a Driver.
type Option func(*Driver)

// WithSize sets the initial terminal size.
func WithSize(width, height int) Option {
	return func(d *Driver) {
		d.width = width
		d.height = height
	}
}

// WithTrace enables source tracing.
func WithTrace(enabled bool) Option {
	return func(d *Driver) {
		d.traceEnabled = enabled
	}
}

// WithConfig sets the screen configuration file path.
func WithConfig(path string) Option {
	return func(d *Driver) {
		d.configPath = path
	}
}

// Start initializes the driver and prepares for automation.
func (d *Driver) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.started {
		return fmt.Errorf("driver already started")
	}

	// Initialize the model
	cmd := d.model.Init()
	d.processCmd(cmd)

	// Send initial window size
	d.sendMsg(tea.WindowSizeMsg{Width: d.width, Height: d.height})

	// Capture initial frame
	d.captureFrame()

	d.started = true
	return nil
}

// Stop shuts down the driver.
func (d *Driver) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.started {
		return fmt.Errorf("driver not started")
	}

	d.started = false
	return nil
}

// IsStarted returns true if the driver is running.
func (d *Driver) IsStarted() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.started
}

// SendKey sends a key event to the TUI.
func (d *Driver) SendKey(key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.started {
		return fmt.Errorf("driver not started")
	}

	msg := d.injector.Key(key)
	d.sendMsg(msg)
	d.captureFrame()

	return nil
}

// SendKeys sends multiple key events in sequence.
func (d *Driver) SendKeys(keys ...string) error {
	for _, key := range keys {
		if err := d.SendKey(key); err != nil {
			return err
		}
	}
	return nil
}

// SendText types a string as individual key presses.
func (d *Driver) SendText(text string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.started {
		return fmt.Errorf("driver not started")
	}

	normalized := norm.NFC.String(text)
	d.sendMsg(d.injector.Text(normalized))
	d.captureFrame()

	return nil
}

// Resize changes the terminal dimensions.
func (d *Driver) Resize(width, height int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.started {
		return fmt.Errorf("driver not started")
	}

	d.width = width
	d.height = height
	d.injector.Resize(width, height)
	d.buffer.Resize(width, height)

	msg := d.injector.WindowSize(width, height)
	d.sendMsg(msg)
	d.captureFrame()

	return nil
}

// Click sends a mouse click at the given position.
func (d *Driver) Click(x, y int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.started {
		return fmt.Errorf("driver not started")
	}

	msgs := d.injector.MouseClick(x, y, inject.MouseLeft)
	for _, msg := range msgs {
		d.sendMsg(msg)
	}
	d.captureFrame()

	return nil
}

// Scroll sends a scroll event at the given position.
func (d *Driver) Scroll(x, y int, up bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.started {
		return fmt.Errorf("driver not started")
	}

	msg := d.injector.Scroll(x, y, up)
	d.sendMsg(msg)
	d.captureFrame()

	return nil
}

// GetFrame returns the current rendered frame.
func (d *Driver) GetFrame() *capture.Frame {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.buffer.GetFrame()
}

// GetFrameCount returns the number of frames captured.
func (d *Driver) GetFrameCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.buffer.GetFrameCount()
}

// GetUpdateCount returns the number of Update calls made.
func (d *Driver) GetUpdateCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.updateCount
}

// GetSize returns the current terminal dimensions.
func (d *Driver) GetSize() (width, height int) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.width, d.height
}

// WaitForContent waits for specific text to appear in the frame.
func (d *Driver) WaitForContent(text string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		frame := d.GetFrame()
		if strings.Contains(frame.Content, text) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for content: %q", text)
}

// WaitForLine waits for specific text on a line.
func (d *Driver) WaitForLine(lineNum int, text string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		frame := d.GetFrame()
		line := frame.GetLine(lineNum)
		if strings.Contains(line, text) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for line %d to contain: %q", lineNum, text)
}

// ContainsText checks if the current frame contains the given text.
func (d *Driver) ContainsText(text string) bool {
	frame := d.GetFrame()
	return strings.Contains(frame.Content, text)
}

// GetText returns the text content of the current frame.
func (d *Driver) GetText() string {
	frame := d.GetFrame()
	return frame.Content
}

// GetTextAt returns text from a specific region.
func (d *Driver) GetTextAt(x, y, width, height int) string {
	frame := d.GetFrame()
	return frame.GetText(x, y, width, height)
}

// sendMsg sends a message to the model and processes the result.
func (d *Driver) sendMsg(msg tea.Msg) {
	newModel, cmd := d.model.Update(msg)
	d.model = newModel
	d.updateCount++

	d.processCmd(cmd)
}

// processCmd executes a command and handles the resulting message.
func (d *Driver) processCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}

	// Execute the command
	msg := cmd()
	if msg == nil {
		return
	}

	// Handle batch commands
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			d.processCmd(c)
		}
		return
	}

	// Skip quit messages
	if _, ok := msg.(tea.QuitMsg); ok {
		return
	}

	// Process the message
	d.sendMsg(msg)
}

// ViewStringer is an optional interface that models can implement
// to provide string content directly.
type ViewStringer interface {
	ViewString() string
}

// captureFrame renders the model and captures the output.
func (d *Driver) captureFrame() {
	var content string

	// Check if model implements ViewString() for direct string output
	if vs, ok := d.model.(ViewStringer); ok {
		content = vs.ViewString()
	} else {
		// Fall back to View() and render using ultraviolet
		view := d.model.View()
		content = d.renderView(view)
	}

	d.buffer.SetContent(content)
}

// Model returns the underlying model for direct access.
// This is useful for checking if the model implements specific interfaces.
func (d *Driver) Model() tea.Model {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.model
}

// renderView renders a tea.View to a string.
//
// bubbletea v2 removed the View.Layer field; headless automation now reads
// the pre-composed string content directly. The ultraviolet screen buffer
// is no longer needed here since View.Content already carries the final
// frame produced by the chat App.View.
func (d *Driver) renderView(view tea.View) string {
	return view.Content
}

// LoadConfig loads screen configuration from a file.
func (d *Driver) LoadConfig(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	d.screenConfig = cfg
	d.navigator = config.NewNavigator(cfg)
	return nil
}

// SetConfig sets the screen configuration directly.
func (d *Driver) SetConfig(cfg *config.Config) {
	d.screenConfig = cfg
	d.navigator = config.NewNavigator(cfg)
}

// Navigate navigates to a target screen using configured key sequences.
func (d *Driver) Navigate(targetScreen string) error {
	if d.navigator == nil {
		return fmt.Errorf("no screen configuration loaded")
	}

	// Detect current screen if not set
	if d.navigator.GetCurrentScreen() == "" {
		detected := d.navigator.DetectScreen(d.GetText())
		if detected != "" {
			d.navigator.SetCurrentScreen(detected)
		} else {
			return fmt.Errorf("cannot detect current screen")
		}
	}

	// Get navigation keys
	keys, err := d.navigator.GetKeysTo(targetScreen)
	if err != nil {
		return err
	}

	// Send keys
	for _, key := range keys {
		if err := d.SendKey(key); err != nil {
			return err
		}
	}

	// Update current screen
	d.navigator.SetCurrentScreen(targetScreen)
	return nil
}

// WaitForScreen waits for a specific screen to be detected.
func (d *Driver) WaitForScreen(screenID string, timeout time.Duration) error {
	if d.navigator == nil {
		return fmt.Errorf("no screen configuration loaded")
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		detected := d.navigator.DetectScreen(d.GetText())
		if detected == screenID {
			d.navigator.SetCurrentScreen(screenID)
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for screen: %s", screenID)
}

// GetCurrentScreen returns the current detected screen.
func (d *Driver) GetCurrentScreen() string {
	if d.navigator == nil {
		return ""
	}
	return d.navigator.GetCurrentScreen()
}

// DetectScreen attempts to detect the current screen from content.
func (d *Driver) DetectScreen() string {
	if d.navigator == nil {
		return ""
	}
	detected := d.navigator.DetectScreen(d.GetText())
	if detected != "" {
		d.navigator.SetCurrentScreen(detected)
	}
	return detected
}

// GetScreenConfig returns the loaded screen configuration.
func (d *Driver) GetScreenConfig() *config.Config {
	return d.screenConfig
}

// GetRegion returns a region by ID from the current screen.
func (d *Driver) GetRegion(regionID string) *config.Region {
	if d.screenConfig == nil {
		return nil
	}

	currentScreen := d.GetCurrentScreen()
	if currentScreen == "" {
		// Search all screens
		_, region := d.screenConfig.FindRegion(regionID)
		return region
	}

	screen := d.screenConfig.GetScreen(currentScreen)
	if screen == nil {
		return nil
	}

	for i := range screen.Regions {
		if screen.Regions[i].ID == regionID {
			return &screen.Regions[i]
		}
	}
	return nil
}

// ClickRegion clicks on a region by ID.
func (d *Driver) ClickRegion(regionID string) error {
	region := d.GetRegion(regionID)
	if region == nil {
		return fmt.Errorf("region not found: %s", regionID)
	}

	// Resolve bounds relative to current viewport
	bounds := region.Bounds.Resolve(d.width, d.height)

	// Click center of region
	x := bounds.X + bounds.Width/2
	y := bounds.Y + bounds.Height/2

	return d.Click(x, y)
}
