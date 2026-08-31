// Package ii provides browser automation tools ported from ii-agent.
// This file implements shared browser session management using chromedp.
//
// The BrowserManager provides a centralized way to manage browser sessions,
// tabs, and state across multiple browser tools. It follows the patterns
// established in the Python ii-agent implementation.
package ii

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// BrowserConfig contains configuration options for the browser session.
type BrowserConfig struct {
	// Headless runs the browser in headless mode (no visible window)
	Headless bool

	// ViewportWidth is the browser viewport width in pixels
	ViewportWidth int

	// ViewportHeight is the browser viewport height in pixels
	ViewportHeight int

	// UserAgent is the custom user agent string (empty for default)
	UserAgent string

	// Timeout is the default timeout for operations
	Timeout time.Duration

	// ChromePath is the path to the Chrome/Chromium executable (empty for auto-detect)
	ChromePath string

	// DisableGPU disables GPU hardware acceleration
	DisableGPU bool

	// NoSandbox disables the Chrome sandbox (required for some environments)
	NoSandbox bool
}

// DefaultBrowserConfig returns the default browser configuration.
func DefaultBrowserConfig() *BrowserConfig {
	return &BrowserConfig{
		Headless:       true,
		ViewportWidth:  1280,
		ViewportHeight: 720,
		Timeout:        30 * time.Second,
		DisableGPU:     true,
		NoSandbox:      false,
	}
}

// Viewport represents the browser viewport dimensions.
type Viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// InteractiveElement represents an interactive DOM element on the page.
type InteractiveElement struct {
	Index          int     `json:"index"`
	TagName        string  `json:"tag_name"`
	InputType      string  `json:"input_type,omitempty"`
	Text           string  `json:"text"`
	BrowserAgentID string  `json:"browser_agent_id"`
	X              float64 `json:"x"`
	Y              float64 `json:"y"`
	Width          float64 `json:"width"`
	Height         float64 `json:"height"`
}

// BrowserState represents the current state of the browser.
type BrowserState struct {
	// URL is the current page URL
	URL string `json:"url"`

	// Title is the current page title
	Title string `json:"title"`

	// Screenshot is the base64-encoded PNG screenshot
	Screenshot string `json:"screenshot,omitempty"`

	// ScreenshotWithHighlights is the screenshot with interactive elements highlighted
	ScreenshotWithHighlights string `json:"screenshot_with_highlights,omitempty"`

	// Viewport contains the current viewport dimensions
	Viewport Viewport `json:"viewport"`

	// InteractiveElements maps element index to element info
	InteractiveElements map[int]*InteractiveElement `json:"interactive_elements,omitempty"`

	// TabCount is the number of open tabs
	TabCount int `json:"tab_count"`

	// CurrentTabIndex is the index of the current tab
	CurrentTabIndex int `json:"current_tab_index"`
}

// BrowserManager manages a browser session for automation.
// It provides thread-safe access to browser operations and maintains
// the current browser state including screenshots and interactive elements.
type BrowserManager struct {
	mu sync.RWMutex

	// allocCtx is the browser allocator context
	allocCtx context.Context

	// allocCancel cancels the allocator
	allocCancel context.CancelFunc

	// ctx is the current browser context
	ctx context.Context

	// cancel cancels the browser context
	cancel context.CancelFunc

	// config holds the browser configuration
	config *BrowserConfig

	// state holds the current browser state
	state *BrowserState

	// targets holds all open tab targets

	// currentTargetIdx is the index of the current tab
	currentTargetIdx int

	// isRunning indicates if the browser is running
	isRunning bool
}

// NewBrowserManager creates a new browser manager with the given configuration.
// If config is nil, default configuration is used.
func NewBrowserManager(config *BrowserConfig) *BrowserManager {
	if config == nil {
		config = DefaultBrowserConfig()
	}
	return &BrowserManager{
		config: config,
		state: &BrowserState{
			Viewport: Viewport{
				Width:  config.ViewportWidth,
				Height: config.ViewportHeight,
			},
			InteractiveElements: make(map[int]*InteractiveElement),
		},
	}
}

// Start initializes and starts the browser session.
func (bm *BrowserManager) Start(ctx context.Context) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.isRunning {
		return nil
	}

	// Build Chrome options
	opts := []chromedp.ExecAllocatorOption{
		chromedp.Flag("headless", bm.config.Headless),
		chromedp.Flag("disable-gpu", bm.config.DisableGPU),
		chromedp.WindowSize(bm.config.ViewportWidth, bm.config.ViewportHeight),
	}

	if bm.config.NoSandbox {
		opts = append(opts, chromedp.Flag("no-sandbox", true))
	}

	if bm.config.UserAgent != "" {
		opts = append(opts, chromedp.UserAgent(bm.config.UserAgent))
	}

	if bm.config.ChromePath != "" {
		opts = append(opts, chromedp.ExecPath(bm.config.ChromePath))
	}

	// Append default options
	opts = append(chromedp.DefaultExecAllocatorOptions[:], opts...)

	// Create allocator
	bm.allocCtx, bm.allocCancel = chromedp.NewExecAllocator(ctx, opts...)

	// Create browser context
	bm.ctx, bm.cancel = chromedp.NewContext(bm.allocCtx)

	// Set timeout
	bm.ctx, bm.cancel = context.WithTimeout(bm.ctx, bm.config.Timeout)

	// Navigate to about:blank to start the browser
	if err := chromedp.Run(bm.ctx, chromedp.Navigate("about:blank")); err != nil {
		bm.allocCancel()
		return fmt.Errorf("failed to start browser: %w", err)
	}

	bm.isRunning = true
	return nil
}

// Stop closes the browser session and releases resources.
func (bm *BrowserManager) Stop() error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.isRunning {
		return nil
	}

	if bm.cancel != nil {
		bm.cancel()
	}
	if bm.allocCancel != nil {
		bm.allocCancel()
	}

	bm.isRunning = false
	bm.ctx = nil
	bm.allocCtx = nil
	return nil
}

// Restart stops and starts the browser session.
func (bm *BrowserManager) Restart(ctx context.Context) error {
	if err := bm.Stop(); err != nil {
		return err
	}
	return bm.Start(ctx)
}

// IsRunning returns whether the browser is currently running.
func (bm *BrowserManager) IsRunning() bool {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	return bm.isRunning
}

// GetState returns the current browser state.
func (bm *BrowserManager) State() *BrowserState {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	return bm.state
}

// Context returns the current browser context.
// Returns an error if the browser is not running.
func (bm *BrowserManager) Context() (context.Context, error) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	if !bm.isRunning || bm.ctx == nil {
		return nil, fmt.Errorf("browser is not running")
	}
	return bm.ctx, nil
}

// Navigate navigates to the specified URL.
func (bm *BrowserManager) Navigate(ctx context.Context, urlStr string) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.isRunning {
		return fmt.Errorf("browser is not running")
	}

	// Validate URL
	if _, err := url.Parse(urlStr); err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Navigate with domcontentloaded wait
	if err := chromedp.Run(bm.ctx,
		chromedp.Navigate(urlStr),
		chromedp.WaitReady("body"),
	); err != nil {
		return fmt.Errorf("navigation failed: %w", err)
	}

	// Small delay for dynamic content
	time.Sleep(1500 * time.Millisecond)

	return nil
}

// UpdateState captures the current browser state including URL, title, and screenshot.
func (bm *BrowserManager) UpdateState(ctx context.Context) (*BrowserState, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.isRunning {
		return nil, fmt.Errorf("browser is not running")
	}

	var urlStr, title string
	var screenshotBuf []byte

	// Get current URL and title
	if err := chromedp.Run(bm.ctx,
		chromedp.Location(&urlStr),
		chromedp.Title(&title),
	); err != nil {
		return nil, fmt.Errorf("failed to get page info: %w", err)
	}

	// Take screenshot
	if err := chromedp.Run(bm.ctx,
		chromedp.CaptureScreenshot(&screenshotBuf),
	); err != nil {
		return nil, fmt.Errorf("failed to capture screenshot: %w", err)
	}

	bm.state.URL = urlStr
	bm.state.Title = title
	bm.state.Screenshot = base64.StdEncoding.EncodeToString(screenshotBuf)

	return bm.state, nil
}

// UpdateStateWithInteractiveElements updates state and identifies interactive elements.
func (bm *BrowserManager) UpdateStateWithInteractiveElements(ctx context.Context) (*BrowserState, error) {
	// First update basic state
	if _, err := bm.UpdateState(ctx); err != nil {
		return nil, err
	}

	bm.mu.Lock()
	defer bm.mu.Unlock()

	// JavaScript to find and mark interactive elements
	findInteractiveJS := `
(function() {
    const interactiveSelectors = [
        'a', 'button', 'input', 'select', 'textarea',
        '[role="button"]', '[role="link"]', '[role="checkbox"]',
        '[role="radio"]', '[role="menuitem"]', '[role="tab"]',
        '[onclick]', '[tabindex]'
    ];

    const elements = [];
    let index = 0;

    interactiveSelectors.forEach(selector => {
        document.querySelectorAll(selector).forEach(el => {
            if (el.offsetWidth > 0 && el.offsetHeight > 0) {
                const rect = el.getBoundingClientRect();
                const id = 'browser-agent-' + index;
                el.setAttribute('data-browser-agent-id', id);

                elements.push({
                    index: index,
                    tag_name: el.tagName.toLowerCase(),
                    input_type: el.type || '',
                    text: (el.innerText || el.value || el.placeholder || '').substring(0, 100),
                    browser_agent_id: id,
                    x: rect.x,
                    y: rect.y,
                    width: rect.width,
                    height: rect.height
                });
                index++;
            }
        });
    });

    return elements;
})()
`

	var elementsJSON []map[string]any
	if err := chromedp.Run(bm.ctx,
		chromedp.Evaluate(findInteractiveJS, &elementsJSON),
	); err != nil {
		return nil, fmt.Errorf("failed to find interactive elements: %w", err)
	}

	// Parse elements
	bm.state.InteractiveElements = make(map[int]*InteractiveElement)
	for _, el := range elementsJSON {
		idx := int(el["index"].(float64))
		bm.state.InteractiveElements[idx] = &InteractiveElement{
			Index:          idx,
			TagName:        el["tag_name"].(string),
			InputType:      el["input_type"].(string),
			Text:           el["text"].(string),
			BrowserAgentID: el["browser_agent_id"].(string),
			X:              el["x"].(float64),
			Y:              el["y"].(float64),
			Width:          el["width"].(float64),
			Height:         el["height"].(float64),
		}
	}

	return bm.state, nil
}

// Click performs a mouse click at the specified coordinates.
func (bm *BrowserManager) Click(ctx context.Context, x, y float64, doubleClick bool) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.isRunning {
		return fmt.Errorf("browser is not running")
	}

	var actions []chromedp.Action
	if doubleClick {
		actions = append(actions, chromedp.MouseClickXY(x, y, chromedp.ClickCount(2)))
	} else {
		actions = append(actions, chromedp.MouseClickXY(x, y))
	}

	if err := chromedp.Run(bm.ctx, actions...); err != nil {
		return fmt.Errorf("click failed: %w", err)
	}

	// Wait for any resulting actions
	time.Sleep(1 * time.Second)
	return nil
}

// TypeText types text using the keyboard.
func (bm *BrowserManager) TypeText(ctx context.Context, text string) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.isRunning {
		return fmt.Errorf("browser is not running")
	}

	if err := chromedp.Run(bm.ctx,
		chromedp.KeyEvent(text, chromedp.KeyModifiers(0)),
	); err != nil {
		// Fallback to SendKeys if KeyEvent fails
		if err := chromedp.Run(bm.ctx, chromedp.SendKeys("", text, chromedp.ByQuery)); err != nil {
			return fmt.Errorf("type text failed: %w", err)
		}
	}

	return nil
}

// PressKey simulates pressing a key or key combination.
func (bm *BrowserManager) PressKey(ctx context.Context, key string) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.isRunning {
		return fmt.Errorf("browser is not running")
	}

	// Map key names to chromedp key codes
	keyCode := mapKeyName(key)

	if err := chromedp.Run(bm.ctx,
		chromedp.KeyEvent(keyCode),
	); err != nil {
		return fmt.Errorf("press key failed: %w", err)
	}

	time.Sleep(500 * time.Millisecond)
	return nil
}

// SelectAll selects all content (Ctrl+A / Cmd+A).
func (bm *BrowserManager) SelectAll(ctx context.Context) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.isRunning {
		return fmt.Errorf("browser is not running")
	}

	// Use JavaScript to select all
	selectAllJS := `document.execCommand('selectAll', false, null);`
	if err := chromedp.Run(bm.ctx, chromedp.Evaluate(selectAllJS, nil)); err != nil {
		return fmt.Errorf("select all failed: %w", err)
	}

	return nil
}

// ClearInput clears the current input field.
func (bm *BrowserManager) ClearInput(ctx context.Context) error {
	// Select all and delete
	if err := bm.SelectAll(ctx); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	return bm.PressKey(ctx, "Backspace")
}

// Scroll scrolls the page in the specified direction.
// deltaX and deltaY are in pixels (negative for up/left, positive for down/right).
func (bm *BrowserManager) Scroll(ctx context.Context, deltaX, deltaY float64) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.isRunning {
		return fmt.Errorf("browser is not running")
	}

	// Move mouse to center of viewport first
	centerX := float64(bm.config.ViewportWidth) / 2
	centerY := float64(bm.config.ViewportHeight) / 2

	if err := chromedp.Run(bm.ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			return input.DispatchMouseEvent(input.MouseMoved, centerX, centerY).Do(ctx)
		}),
	); err != nil {
		return fmt.Errorf("move mouse failed: %w", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Dispatch scroll wheel event
	if err := chromedp.Run(bm.ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			return input.DispatchMouseEvent(input.MouseWheel, centerX, centerY).
				WithDeltaX(deltaX).
				WithDeltaY(deltaY).
				Do(ctx)
		}),
	); err != nil {
		return fmt.Errorf("scroll failed: %w", err)
	}

	time.Sleep(100 * time.Millisecond)
	return nil
}

// ScrollDown scrolls the page down by viewport height.
func (bm *BrowserManager) ScrollDown(ctx context.Context) error {
	return bm.Scroll(ctx, 0, float64(bm.config.ViewportHeight)*0.8)
}

// ScrollUp scrolls the page up by viewport height.
func (bm *BrowserManager) ScrollUp(ctx context.Context) error {
	return bm.Scroll(ctx, 0, -float64(bm.config.ViewportHeight)*0.8)
}

// Drag performs a drag and drop operation from start to end coordinates.
func (bm *BrowserManager) Drag(ctx context.Context, startX, startY, endX, endY float64) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.isRunning {
		return fmt.Errorf("browser is not running")
	}

	// Move to start position
	if err := chromedp.Run(bm.ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			return input.DispatchMouseEvent(input.MouseMoved, startX, startY).Do(ctx)
		}),
	); err != nil {
		return fmt.Errorf("move to start failed: %w", err)
	}

	// Mouse down
	if err := chromedp.Run(bm.ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			return input.DispatchMouseEvent(input.MousePressed, startX, startY).
				WithButton(input.Left).
				WithClickCount(1).
				Do(ctx)
		}),
	); err != nil {
		return fmt.Errorf("mouse down failed: %w", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Move to end position
	if err := chromedp.Run(bm.ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			return input.DispatchMouseEvent(input.MouseMoved, endX, endY).Do(ctx)
		}),
	); err != nil {
		return fmt.Errorf("move to end failed: %w", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Mouse up
	if err := chromedp.Run(bm.ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			return input.DispatchMouseEvent(input.MouseReleased, endX, endY).
				WithButton(input.Left).
				WithClickCount(1).
				Do(ctx)
		}),
	); err != nil {
		return fmt.Errorf("mouse up failed: %w", err)
	}

	time.Sleep(1 * time.Second)
	return nil
}

// GetSelectOptions retrieves options from a select element by its browser agent ID.
func (bm *BrowserManager) SelectOptions(ctx context.Context, browserAgentID string) ([]SelectOption, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.isRunning {
		return nil, fmt.Errorf("browser is not running")
	}

	getOptionsJS := fmt.Sprintf(`
(function() {
    const select = document.querySelector('[data-browser-agent-id="%s"]');
    if (!select) return null;
    if (select.tagName.toLowerCase() !== 'select') return {error: 'Element is not a select'};

    return {
        options: Array.from(select.options).map((opt, i) => ({
            text: opt.text,
            value: opt.value,
            index: i,
            selected: opt.selected
        })),
        id: select.id,
        name: select.name
    };
})()
`, browserAgentID)

	var result map[string]any
	if err := chromedp.Run(bm.ctx, chromedp.Evaluate(getOptionsJS, &result)); err != nil {
		return nil, fmt.Errorf("failed to get select options: %w", err)
	}

	if result == nil {
		return nil, fmt.Errorf("select element not found")
	}
	if errMsg, ok := result["error"].(string); ok {
		return nil, fmt.Errorf("%s", errMsg)
	}

	optionsData := result["options"].([]any)
	options := make([]SelectOption, len(optionsData))
	for i, opt := range optionsData {
		optMap := opt.(map[string]any)
		options[i] = SelectOption{
			Text:     optMap["text"].(string),
			Value:    optMap["value"].(string),
			Index:    int(optMap["index"].(float64)),
			Selected: optMap["selected"].(bool),
		}
	}

	return options, nil
}

// SelectOption represents an option in a select element.
type SelectOption struct {
	Text     string `json:"text"`
	Value    string `json:"value"`
	Index    int    `json:"index"`
	Selected bool   `json:"selected"`
}

// SelectDropdownOption selects an option from a select element by text.
func (bm *BrowserManager) SelectDropdownOption(ctx context.Context, browserAgentID, optionText string) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.isRunning {
		return fmt.Errorf("browser is not running")
	}

	selectOptionJS := fmt.Sprintf(`
(function() {
    const select = document.querySelector('[data-browser-agent-id="%s"]');
    if (!select) return {success: false, error: 'Select element not found'};
    if (select.tagName.toLowerCase() !== 'select') return {success: false, error: 'Element is not a select'};

    for (let i = 0; i < select.options.length; i++) {
        if (select.options[i].text === %q) {
            select.options[i].selected = true;
            select.dispatchEvent(new Event('change', {bubbles: true}));
            return {success: true, value: select.options[i].value, index: i};
        }
    }

    return {
        success: false,
        error: 'Option not found',
        availableOptions: Array.from(select.options).map(o => o.text)
    };
})()
`, browserAgentID, optionText)

	var result map[string]any
	if err := chromedp.Run(bm.ctx, chromedp.Evaluate(selectOptionJS, &result)); err != nil {
		return fmt.Errorf("failed to select option: %w", err)
	}

	if success, ok := result["success"].(bool); !ok || !success {
		errMsg := "unknown error"
		if e, ok := result["error"].(string); ok {
			errMsg = e
		}
		return fmt.Errorf("%s", errMsg)
	}

	return nil
}

// GetTabCount returns the number of open tabs.
func (bm *BrowserManager) TabCount(ctx context.Context) (int, error) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	if !bm.isRunning {
		return 0, fmt.Errorf("browser is not running")
	}

	targets, err := chromedp.Targets(bm.ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get targets: %w", err)
	}

	count := 0
	for _, t := range targets {
		if t.Type == "page" {
			count++
		}
	}
	return count, nil
}

// CreateNewTab creates a new browser tab.
func (bm *BrowserManager) CreateNewTab(ctx context.Context) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.isRunning {
		return fmt.Errorf("browser is not running")
	}

	// Create new target (tab)
	if err := chromedp.Run(bm.ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.CreateIsolatedWorld("").Do(ctx)
			return err
		}),
	); err != nil {
		// Fallback: use JavaScript to open new tab
		newTabJS := `window.open('about:blank', '_blank');`
		if err := chromedp.Run(bm.ctx, chromedp.Evaluate(newTabJS, nil)); err != nil {
			return fmt.Errorf("failed to create new tab: %w", err)
		}
	}

	time.Sleep(500 * time.Millisecond)
	return nil
}

// SwitchToTab switches to the tab at the specified index.
func (bm *BrowserManager) SwitchToTab(ctx context.Context, index int) error {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if !bm.isRunning {
		return fmt.Errorf("browser is not running")
	}

	targets, err := chromedp.Targets(bm.ctx)
	if err != nil {
		return fmt.Errorf("failed to get targets: %w", err)
	}

	// Filter to page targets
	var pageTargets []*target.Info
	for _, t := range targets {
		if t.Type == "page" {
			pageTargets = append(pageTargets, t)
		}
	}

	if len(pageTargets) == 0 {
		return fmt.Errorf("no tabs available")
	}

	// Handle negative index (like Python's -1 for last element)
	actualIdx := index
	if index < 0 {
		actualIdx = len(pageTargets) + index
	}

	if actualIdx < 0 || actualIdx >= len(pageTargets) {
		return fmt.Errorf("tab index %d out of range (0-%d)", index, len(pageTargets)-1)
	}

	// Switch to target - get the target and use its ID
	targetInfo := pageTargets[actualIdx]
	if err := chromedp.Run(bm.ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Activate the target
			return target.ActivateTarget(targetInfo.TargetID).Do(ctx)
		}),
	); err != nil {
		return fmt.Errorf("failed to switch to tab: %w", err)
	}

	bm.currentTargetIdx = actualIdx
	time.Sleep(500 * time.Millisecond)
	return nil
}

// Wait pauses execution for the specified duration.
func (bm *BrowserManager) Wait(duration time.Duration) {
	time.Sleep(duration)
}

// IsPDFURL checks if the URL points to a PDF document.
func IsPDFURL(urlStr string) bool {
	lowerURL := strings.ToLower(urlStr)
	return strings.HasSuffix(lowerURL, ".pdf") ||
		strings.Contains(lowerURL, "application/pdf") ||
		strings.Contains(lowerURL, "/pdf/")
}

// HandlePDFNavigation handles navigation to PDF URLs.
func (bm *BrowserManager) HandlePDFNavigation(ctx context.Context) (*BrowserState, error) {
	bm.mu.RLock()
	isPDF := IsPDFURL(bm.state.URL)
	bm.mu.RUnlock()

	if !isPDF {
		return bm.State(), nil
	}

	// For PDFs, we just update state - actual PDF rendering would need
	// additional handling depending on requirements
	return bm.UpdateState(ctx)
}

// mapKeyName maps key names to chromedp-compatible key strings.
func mapKeyName(key string) string {
	// Handle key combinations (e.g., "Control+Enter")
	if strings.Contains(key, "+") {
		parts := strings.Split(key, "+")
		var result []string
		for _, part := range parts {
			result = append(result, mapSingleKey(strings.TrimSpace(part)))
		}
		return strings.Join(result, "+")
	}
	return mapSingleKey(key)
}

// mapSingleKey maps a single key name to its chromedp representation.
func mapSingleKey(key string) string {
	keyMap := map[string]string{
		"Enter":         "\r",
		"Tab":           "\t",
		"Backspace":     "\b",
		"Delete":        "\u007f",
		"Escape":        "\u001b",
		"ArrowUp":       "\u0026",
		"ArrowDown":     "\u0028",
		"ArrowLeft":     "\u0025",
		"ArrowRight":    "\u0027",
		"PageUp":        "PageUp",
		"PageDown":      "PageDown",
		"Home":          "Home",
		"End":           "End",
		"Control":       "Control",
		"Ctrl":          "Control",
		"Alt":           "Alt",
		"Shift":         "Shift",
		"Meta":          "Meta",
		"Command":       "Meta",
		"ControlOrMeta": "Control", // Use Control on non-Mac
	}

	if mapped, ok := keyMap[key]; ok {
		return mapped
	}
	return key
}

// Ensure BrowserManager doesn't use unimported packages
var (
	_ = cdp.NodeID(0)
	_ = dom.GetDocument()
	_ = runtime.Evaluate("")
)
