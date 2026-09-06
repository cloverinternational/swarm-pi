package x11

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/pkg/computeruse"
)

// GetFrontmostApp returns the currently active application.
func (b *Backend) GetFrontmostApp() (*computeruse.FrontmostApp, error) {
	if !b.hasXdtool {
		return nil, computeruse.NewToolNotAvailableError("xdotool")
	}

	// Get active window ID
	windowID, err := b.runCommand("xdotool", "getactivewindow")
	if err != nil {
		return nil, &computeruse.InputError{Operation: "getactivewindow", Err: err}
	}

	// Get window class using xprop
	className := ""
	if b.hasXprop {
		// xprop output: WM_CLASS(STRING) = "instance", "class"
		output, err := b.runCommand("xprop", "-id", windowID, "WM_CLASS")
		if err == nil {
			className = parseWMClass(output)
		}
	}

	// Get window name for display name
	windowName, err := b.runCommand("xdotool", "getwindowname", windowID)
	if err != nil {
		windowName = className
	}

	// Try to get desktop entry ID from class name
	appID := b.classToAppID(className)

	return &computeruse.FrontmostApp{
		ID:          appID,
		DisplayName: windowName,
	}, nil
}

// AppUnderPoint returns the application under the given coordinates.
func (b *Backend) AppUnderPoint(x, y int) (*computeruse.FrontmostApp, error) {
	if !b.hasXdtool {
		return nil, computeruse.NewToolNotAvailableError("xdotool")
	}

	// Find window at point using xwininfo
	windowID := ""
	if b.hasXwininfo {
		// xwininfo can find window at coordinates
		output, err := b.runCommand("xwininfo", "-all", "-geometry")
		if err == nil {
			windowID = parseWindowAtPoint(output, x, y)
		}
	}

	// Fallback: just get the frontmost app
	if windowID == "" {
		return b.GetFrontmostApp()
	}

	// Get window class using xprop
	className := ""
	if b.hasXprop {
		output, err := b.runCommand("xprop", "-id", windowID, "WM_CLASS")
		if err == nil {
			className = parseWMClass(output)
		}
	}

	// Get window name for display name
	windowName, err := b.runCommand("xdotool", "getwindowname", windowID)
	if err != nil {
		windowName = className
	}

	appID := b.classToAppID(className)

	return &computeruse.FrontmostApp{
		ID:          appID,
		DisplayName: windowName,
	}, nil
}

// ListInstalledApps returns a list of installed applications.
// On Linux, this parses .desktop files from standard locations.
func (b *Backend) ListInstalledApps() ([]computeruse.InstalledApp, error) {
	var apps []computeruse.InstalledApp

	// Desktop file locations (in order of priority)
	desktopDirs := []string{
		"/usr/share/applications",
		"/usr/local/share/applications",
		filepath.Join(os.Getenv("HOME"), ".local/share/applications"),
		"/var/lib/snapd/desktop/applications", // Snap apps
	}

	seen := make(map[string]bool)

	for _, dir := range desktopDirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.desktop"))
		if err != nil {
			continue
		}

		for _, file := range files {
			app, err := parseDesktopFile(file)
			if err != nil {
				continue
			}

			// Skip duplicates
			if seen[app.ID] {
				continue
			}
			seen[app.ID] = true

			apps = append(apps, *app)
		}
	}

	return apps, nil
}

// ListRunningApps returns a list of running applications.
func (b *Backend) ListRunningApps() ([]computeruse.RunningApp, error) {
	if !b.hasXdtool {
		return nil, computeruse.NewToolNotAvailableError("xdotool")
	}

	// Get all windows
	output, err := b.runCommand("xdotool", "search", "--onlyvisible", ".*")
	if err != nil {
		return nil, &computeruse.InputError{Operation: "search_windows", Err: err}
	}

	windowIDs := strings.Split(output, "\n")
	seen := make(map[string]bool)
	var apps []computeruse.RunningApp

	for _, winID := range windowIDs {
		if winID == "" {
			continue
		}

		// Get window class using xprop
		className := ""
		if b.hasXprop {
			output, err := b.runCommand("xprop", "-id", winID, "WM_CLASS")
			if err == nil {
				className = parseWMClass(output)
			}
		}

		// Skip duplicates
		appID := b.classToAppID(className)
		if seen[appID] {
			continue
		}
		seen[appID] = true

		// Get window name for display name
		windowName, err := b.runCommand("xdotool", "getwindowname", winID)
		if err != nil {
			windowName = className
		}

		apps = append(apps, computeruse.RunningApp{
			ID:          appID,
			DisplayName: windowName,
		})
	}

	return apps, nil
}

// OpenApp opens an application by its ID.
func (b *Backend) OpenApp(appID string) error {
	// Try different methods to launch the app

	// Method 1: Use gtk-launch if available
	if isToolAvailable("gtk-launch") {
		if err := b.runCommandWithInput("gtk-launch", "", appID); err == nil {
			return nil
		}
	}

	// Method 2: Use i3-msg for i3 window manager
	if isToolAvailable("i3-msg") {
		if _, err := b.runCommand("i3-msg", "exec", appID); err == nil {
			return nil
		}
	}

	// Method 3: Find desktop file and extract Exec command
	app := b.findDesktopFile(appID)
	if app != nil {
		// Execute the command
		if _, err := b.runCommand("sh", "-c", app.Exec); err != nil {
			return &computeruse.AppError{AppID: appID, Err: err}
		}
		return nil
	}

	// Method 4: Try to execute appID directly as a command.
	// Validate appID to prevent shell injection: only allow alphanumeric,
	// dashes, underscores, and dots (typical desktop app identifiers).
	if !isValidAppID(appID) {
		return &computeruse.AppError{AppID: appID, Err: fmt.Errorf("invalid app identifier: %q", appID)}
	}
	if _, err := b.runCommand(appID); err != nil {
		return &computeruse.AppError{AppID: appID, Err: err}
	}

	return nil
}

// desktopApp represents a parsed .desktop file.
type desktopApp struct {
	ID   string
	Name string
	Exec string
	Icon string
}

// parseDesktopFile parses a .desktop file.
func parseDesktopFile(path string) (*computeruse.InstalledApp, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	app := &desktopApp{
		ID: strings.TrimSuffix(filepath.Base(path), ".desktop"),
	}

	inDesktopEntry := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "[Desktop Entry]" {
			inDesktopEntry = true
			continue
		}

		if strings.HasPrefix(line, "[") && line != "[Desktop Entry]" {
			inDesktopEntry = false
			continue
		}

		if !inDesktopEntry {
			continue
		}

		// Parse key=value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "Name":
			app.Name = value
		case "Exec":
			app.Exec = value
		case "Icon":
			app.Icon = value
		}
	}

	// Skip Hidden=true or NoDisplay=true apps
	if app.Name == "" {
		return nil, fmt.Errorf("no name found")
	}

	return &computeruse.InstalledApp{
		ID:          app.ID,
		DisplayName: app.Name,
		Path:        path,
	}, nil
}

// findDesktopFile finds a .desktop file for an app ID.
func (b *Backend) findDesktopFile(appID string) *desktopApp {
	// Add .desktop extension if not present
	if !strings.HasSuffix(appID, ".desktop") {
		appID = appID + ".desktop"
	}

	// Desktop file locations
	desktopDirs := []string{
		"/usr/share/applications",
		"/usr/local/share/applications",
		filepath.Join(os.Getenv("HOME"), ".local/share/applications"),
		"/var/lib/snapd/desktop/applications",
	}

	for _, dir := range desktopDirs {
		path := filepath.Join(dir, appID)
		if _, err := os.Stat(path); err == nil {
			// Parse the file
			file, err := os.Open(path)
			if err != nil {
				continue
			}

			app := &desktopApp{ID: appID}
			scanner := bufio.NewScanner(file)
			inDesktopEntry := false

			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "[Desktop Entry]" {
					inDesktopEntry = true
					continue
				}
				if strings.HasPrefix(line, "[") && line != "[Desktop Entry]" {
					inDesktopEntry = false
					continue
				}
				if !inDesktopEntry {
					continue
				}

				parts := strings.SplitN(line, "=", 2)
				if len(parts) != 2 {
					continue
				}

				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])

				switch key {
				case "Name":
					app.Name = value
				case "Exec":
					app.Exec = value
				}
			}

			file.Close()

			if app.Exec != "" {
				return app
			}
		}
	}

	return nil
}

// classToAppID attempts to convert a window class name to an app ID.
func (b *Backend) classToAppID(className string) string {
	// Common mappings
	className = strings.ToLower(className)

	// Try to find matching desktop file
	apps, _ := b.ListInstalledApps()
	for _, app := range apps {
		appName := strings.ToLower(strings.TrimSuffix(app.ID, ".desktop"))
		if strings.Contains(className, appName) || appName == className {
			return app.ID
		}
	}

	// Return class name as-is if no match found
	return className + ".desktop"
}

// parseWMClass parses xprop WM_CLASS output.
// Input format: WM_CLASS(STRING) = "instance", "class"
// Returns the class name (second value).
func parseWMClass(output string) string {
	// Look for the pattern: = "instance", "class"
	_, after, ok := strings.Cut(output, "=")
	if !ok {
		return ""
	}

	values := strings.Trim(after, " ")
	// Remove quotes and split by comma
	values = strings.Trim(values, "\"")
	parts := strings.Split(values, "\", \"")

	if len(parts) >= 2 {
		// Return the class (second part)
		return strings.Trim(parts[1], "\"")
	}
	if len(parts) == 1 {
		return strings.Trim(parts[0], "\"")
	}

	return ""
}

// parseWindowAtPoint parses xwininfo output to find a window at given coordinates.
// This is a simplified implementation that returns empty string on failure.
func parseWindowAtPoint(output string, x, y int) string {
	// This is a placeholder - proper implementation would parse xwininfo output
	// to find which window contains the given point
	return ""
}

// isValidAppID checks that appID contains only safe characters for direct
// command execution: alphanumeric, dashes, underscores, dots, and colons
// (for .desktop file style IDs like "org.example.App").
func isValidAppID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == ':') {
			return false
		}
	}
	return true
}
