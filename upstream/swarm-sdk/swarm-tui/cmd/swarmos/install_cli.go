package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/update"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
)

// installCLIDeps holds dependencies for the install CLI (for testing)
type installCLIDeps struct {
	stdout         io.Writer
	stderr         io.Writer
	stdin          func() io.Reader
	newChecker     func() *update.Checker
	runMakeInstall func() error
}

func defaultInstallCLIDeps() installCLIDeps {
	return installCLIDeps{
		stdout:     os.Stdout,
		stderr:     os.Stderr,
		stdin:      func() io.Reader { return os.Stdin },
		newChecker: func() *update.Checker { return update.NewChecker(update.DefaultGitHubRepo) },
		runMakeInstall: func() error {
			// Run make install - the existing install.sh script handles the animation
			cmd := exec.Command("make", "install")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin
			return cmd.Run()
		},
	}
}

// runDevInstall runs make install with a clean animation
// For dev mode, we run make install and let the existing install.sh handle the animation
// but we suppress the build output
func runDevInstall(deps installCLIDeps, installDir string) error {
	// Get current version for display
	currentVersion := version.Version

	// Show our header first
	printInstallHeader(currentVersion+" (dev)", installDir, deps.stdout)

	// Run make build silently (suppress output but capture errors)
	// We redirect stdout/stderr to a pipe so we can show errors if needed
	buildCmd := exec.Command("make", "build")
	buildCmd.Stdout = nil
	buildCmd.Stderr = nil
	if err := buildCmd.Run(); err != nil {
		fmt.Fprintf(deps.stdout, "\n\033[91m✗ Build failed: %v\033[0m\n", err)
		return err
	}

	// Now run make install which has the nice animation
	// We just need to clear our header and let install.sh take over
	fmt.Fprintf(deps.stdout, "\033[6F\033[J") // Clear our header

	// Set environment for install
	os.Setenv("INSTALL_DIR", installDir)

	cmd := exec.Command("./scripts/install.sh", installDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// runInstallCLI handles the 'swarm install' subcommand
func runInstallCLI(args []string) error {
	// Auto-detect and set GitHub token from gh CLI if available
	ensureGitHubToken()
	return runInstallCLIWithDeps(args, defaultInstallCLIDeps())
}

// ensureGitHubToken ensures GITHUB_TOKEN is set, using gh CLI if available
func ensureGitHubToken() {
	// Check if already set
	if os.Getenv("GITHUB_TOKEN") != "" || os.Getenv("GH_TOKEN") != "" {
		return
	}

	// Try to get token from gh CLI
	if cmd := exec.Command("gh", "auth", "token"); cmd != nil {
		if output, err := cmd.Output(); err == nil {
			token := strings.TrimSpace(string(output))
			if token != "" {
				os.Setenv("GITHUB_TOKEN", token)
			}
		}
	}
}

func runInstallCLIWithDeps(args []string, deps installCLIDeps) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(deps.stderr)

	devMode := fs.Bool("d", false, "Dev mode: build and install locally with clean output")
	versionFlag := fs.String("version", "", "Install specific version (e.g., v1.0.5)")
	listVersions := fs.Bool("list", false, "List available versions from GitHub releases")
	installDir := fs.String("dir", "/usr/local/bin", "Installation directory")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Handle --list flag
	if *listVersions {
		return listAvailableVersions(deps)
	}

	// Handle --version flag
	targetVersion := strings.TrimSpace(*versionFlag)
	if fs.NArg() > 0 && targetVersion == "" {
		// Positional argument: swarm install v1.0.5
		targetVersion = strings.TrimSpace(fs.Arg(0))
	}

	// Dev mode: run make install with clean animation
	if *devMode {
		return runDevInstall(deps, *installDir)
	}

	// Specific version: download from GitHub releases
	if targetVersion != "" {
		return runVersionInstall(deps, targetVersion, *installDir)
	}

	// Default: install latest from GitHub releases
	return runLatestInstall(deps, *installDir)
}

// listAvailableVersions shows available versions from GitHub releases
func listAvailableVersions(deps installCLIDeps) error {
	fmt.Fprintf(deps.stdout, "Fetching available versions...\n")

	checker := deps.newChecker()
	releases, err := checker.GetReleases(context.Background(), 20)
	if err != nil {
		return fmt.Errorf("failed to fetch releases: %w", err)
	}

	fmt.Fprintf(deps.stdout, "\nAvailable versions:\n")
	for _, release := range releases {
		tag := strings.TrimPrefix(release.TagName, "tui/")
		fmt.Fprintf(deps.stdout, "  %s", tag)
		if release.Prerelease {
			fmt.Fprintf(deps.stdout, " (prerelease)")
		}
		fmt.Fprintf(deps.stdout, "\n")
	}

	return nil
}

// runVersionInstall downloads and installs a specific version
func runVersionInstall(deps installCLIDeps, targetVersion, installDir string) error {
	// Normalize version (but handle edge/dev releases differently)
	// Edge releases: edge-XXXXXXX (don't add 'v' prefix)
	// Regular releases: v1.0.0 (add 'v' if missing)
	if !strings.HasPrefix(targetVersion, "v") && !strings.HasPrefix(targetVersion, "edge-") && !strings.HasPrefix(targetVersion, "dev-") {
		targetVersion = "v" + targetVersion
	}

	checker := deps.newChecker()
	releases, err := checker.GetReleases(context.Background(), 100)
	if err != nil {
		return fmt.Errorf("failed to fetch releases: %w", err)
	}

	// Find the target release
	var targetRelease *update.Release
	for _, release := range releases {
		tag := strings.TrimPrefix(release.TagName, "tui/")
		// For edge releases, also try matching without 'edge-' prefix variations
		if tag == targetVersion {
			targetRelease = release
			break
		}
		// Handle "swarm install edge-XXX" matching "tui/edge-XXX"
		if strings.HasPrefix(targetVersion, "edge-") && tag == targetVersion {
			targetRelease = release
			break
		}
	}

	if targetRelease == nil {
		return fmt.Errorf("version %s not found", targetVersion)
	}

	// Download with animation
	return runInstallWithAnimation(targetVersion, func() error {
		return downloadAndInstallRelease(checker, targetRelease, installDir, deps)
	}, installDir, deps)
}

// runLatestInstall downloads and installs the latest version
func runLatestInstall(deps installCLIDeps, installDir string) error {
	checker := deps.newChecker()

	result, err := checker.CheckForUpdate(context.Background(), version.Version)
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	if !result.UpdateAvailable {
		fmt.Fprintf(deps.stdout, "Already up to date: %s\n", version.Version)
		return nil
	}

	fmt.Fprintf(deps.stdout, "Updating from %s to %s...\n", version.Version, result.LatestVersion)

	// Download with animation
	return runInstallWithAnimation(result.LatestVersion, func() error {
		return downloadAndInstallRelease(checker, result.Release, installDir, deps)
	}, installDir, deps)
}

// downloadAndInstallRelease downloads a release and installs it
func downloadAndInstallRelease(checker *update.Checker, release *update.Release, installDir string, deps installCLIDeps) error {
	binaryAsset, _, err := checker.GetAssetForPlatform(release)
	if err != nil {
		return fmt.Errorf("no binary for platform %s/%s: %w", runtime.GOOS, runtime.GOARCH, err)
	}

	downloader := update.NewDownloader()
	result, err := downloader.DownloadBinary(context.Background(), binaryAsset, nil)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(result.FilePath)

	// Make executable
	if err := os.Chmod(result.FilePath, 0755); err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}

	// Install to target directory
	if err := installBinary(result.FilePath, installDir); err != nil {
		return fmt.Errorf("install failed: %w", err)
	}

	return nil
}

// installBinary copies the binary to the install directory
func installBinary(srcPath, installDir string) error {
	// Check if we need sudo
	needSudo := false
	if _, err := os.Stat(installDir); err == nil {
		if fi, err := os.Stat(installDir); err == nil {
			if fi.Mode().Perm()&0200 == 0 {
				needSudo = true
			}
		}
	}

	binaryName := "swarm"
	destPath := installDir + "/" + binaryName

	if needSudo {
		cmd := exec.Command("sudo", "install", "-m", "0755", srcPath, destPath)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// Ensure directory exists
	if err := os.MkdirAll(installDir, 0755); err != nil {
		return err
	}

	// Copy file
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

// -----------------------------------------------------------------------------
// Progress Animation
// -----------------------------------------------------------------------------

// runInstallWithAnimation runs the installation with an animated progress display
func runInstallWithAnimation(displayVersion string, step func() error, installDir string, deps installCLIDeps) error {
	// Create a context that cancels on Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	// Print header
	printInstallHeader(displayVersion, installDir, deps.stdout)

	// Run installation in background
	errChan := make(chan error, 1)
	go func() {
		errChan <- step()
	}()

	// Show animated progress
	done := false
	spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinnerIdx := 0
	progress := 0

	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	for !done {
		select {
		case <-ctx.Done():
			fmt.Fprintf(deps.stdout, "\n\033[91m✗ Installation cancelled\033[0m\n")
			return fmt.Errorf("installation cancelled")
		case err := <-errChan:
			done = true
			progress = 100
			printInstallProgress(progress, spinner[spinnerIdx], true)
			fmt.Fprintf(deps.stdout, "\n")
			if err != nil {
				fmt.Fprintf(deps.stdout, "\033[91m✗ Error: %s\033[0m\n", err.Error())
			} else {
				printInstallSuccess(displayVersion, installDir, deps.stdout)
			}
		case <-ticker.C:
			if !done {
				progress += 1
				if progress > 95 {
					progress = 95
				}
				spinnerIdx = (spinnerIdx + 1) % len(spinner)
				printInstallProgress(progress, spinner[spinnerIdx], false)
			}
		}
	}

	return nil
}

// printInstallHeader shows the installation header
func printInstallHeader(displayVersion, installDir string, w io.Writer) {
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "\033[96m╭─────────────────────────────────────────────────────────────────╮\033[0m\n")
	fmt.Fprintf(w, "\033[96m│\033[0m  \033[1m\033[97mSwarmOS Installation\033[0m                                    \033[96m│\033[0m\n")
	fmt.Fprintf(w, "\033[96m├─────────────────────────────────────────────────────────────────┤\033[0m\n")
	fmt.Fprintf(w, "\033[96m│\033[0m  \033[2mVersion:\033[0m     \033[96m%-43s\033[0m\033[96m│\033[0m\n", displayVersion)
	fmt.Fprintf(w, "\033[96m│\033[0m  \033[2mInstall to:\033[0m  \033[94m%-43s\033[0m\033[96m│\033[0m\n", installDir)
	fmt.Fprintf(w, "\033[96m│\033[0m                                                                \033[96m│\033[0m\n")
}

// printInstallProgress shows the installation progress
func printInstallProgress(progress int, spinner string, complete bool) {
	// Move cursor to progress line
	fmt.Printf("\033[6F") // Move up 6 lines to progress area

	// Clear and redraw
	width := 30
	filled := width * progress / 100
	empty := width - filled

	var bar strings.Builder
	bar.WriteString("[")
	for i := 0; i < filled; i++ {
		bar.WriteString("█")
	}
	for i := 0; i < empty; i++ {
		bar.WriteString("░")
	}
	bar.WriteString("]")

	// Progress line
	fmt.Printf("\033[2K\033[96m│\033[0m  ")
	if complete {
		fmt.Printf("\033[92m✓\033[0m Complete")
	} else {
		fmt.Printf("\033[93m%s\033[0m Installing %s %3d%%", spinner, bar.String(), progress)
	}

	// Fill rest of line
	fmt.Printf("%*s\033[96m│\033[0m\n", 60-len(bar.String())-15, "")

	// Move back down
	fmt.Printf("\033[5E") // Move down 5 lines
}

// printInstallSuccess shows the success message
func printInstallSuccess(displayVersion, installDir string, w io.Writer) {
	fmt.Fprintf(w, "\033[92m╭─────────────────────────────────────────────────────────────────╮\033[0m\n")
	fmt.Fprintf(w, "\033[92m│\033[0m  \033[1m\033[97mInstallation Complete!\033[0m                                     \033[92m│\033[0m\n")
	fmt.Fprintf(w, "\033[92m├─────────────────────────────────────────────────────────────────┤\033[0m\n")
	fmt.Fprintf(w, "\033[92m│\033[0m  \033[96mswarm\033[0m      \033[92m✓ Ready\033[0m                                    \033[92m│\033[0m\n")
	fmt.Fprintf(w, "\033[92m│\033[0m  \033[2mVersion:\033[0m   \033[96m%s\033[0m\n", displayVersion)
	fmt.Fprintf(w, "\033[92m│\033[0m  \033[2mLocation:\033[0m \033[94m%s\033[0m\n", installDir)
	fmt.Fprintf(w, "\033[92m╰─────────────────────────────────────────────────────────────────╯\033[0m\n")
	fmt.Fprintf(w, "\n")
}
