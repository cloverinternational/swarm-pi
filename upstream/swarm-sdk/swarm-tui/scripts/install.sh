#!/usr/bin/env bash
# Fancy install script for swarm binaries
# Usage: ./scripts/install.sh [INSTALL_DIR]

set -e

# Colors and styling
RESET='\033[0m'
BOLD='\033[1m'
DIM='\033[2m'
ITALIC='\033[3m'
UNDERLINE='\033[4m'

# Bright colors
BRIGHT_BLACK='\033[90m'
BRIGHT_RED='\033[91m'
BRIGHT_GREEN='\033[92m'
BRIGHT_YELLOW='\033[93m'
BRIGHT_BLUE='\033[94m'
BRIGHT_MAGENTA='\033[95m'
BRIGHT_CYAN='\033[96m'
BRIGHT_WHITE='\033[97m'

# Backgrounds
BG_GREEN='\033[42m'
BG_BLUE='\033[44m'

# Unicode box drawing characters
BOX_TOP='╭─────────────────────────────────────────────────────────────────╮'
BOX_BOTTOM='╰─────────────────────────────────────────────────────────────────╯'
BOX_VERTICAL='│'
BOX_T='├'
BOX_T_INV='┤'

# Spinner frames for animation
SPINNER_FRAMES=('⠋' '⠙' '⠹' '⠸' '⠼' '⠴' '⠦' '⠧' '⠇' '⠏')
SPINNER_IDX=0

# Get binary names and version from environment or defaults
BINARY_NAME="${BINARY_NAME:-swarm}"
HEADLESS_BINARY="${HEADLESS_BINARY:-swarm-headless}"
INSTALL_DIR="${1:-/usr/local/bin}"
VERSION="${VERSION:-$(./swarm -version 2>/dev/null | head -1 | cut -d' ' -f3 || echo 'dev')}"
# Guard against the binary being absent or unrunnable so this script never
# hard-fails under `set -e` when invoked before/without a build.
[ -z "$VERSION" ] && VERSION='dev'
BUILD_ID="${BUILD_ID:-$(date +%s)}"

cleanup() {
    tput cnorm 2>/dev/null || true
}
trap cleanup EXIT
tput civis 2>/dev/null || true

# Animation helpers
spinner() {
    printf "${BRIGHT_CYAN}${SPINNER_FRAMES[$SPINNER_IDX]}${RESET}"
    SPINNER_IDX=$(( (SPINNER_IDX + 1) % ${#SPINNER_FRAMES[@]} ))
}

clear_spinner() {
    printf "\r\033[K"
}

# Fancy print functions
print_header() {
    echo ""
    echo -e "${BRIGHT_CYAN}${BOX_TOP}${RESET}"
    echo -e "${BRIGHT_CYAN}${BOX_VERTICAL}${RESET}  ${BOLD}${BRIGHT_WHITE}SwarmOS Installation${RESET}                                    ${BRIGHT_CYAN}${BOX_VERTICAL}${RESET}"
    echo -e "${BRIGHT_CYAN}${BOX_T}─────────────────────────────────────────────────────────────────${BOX_T_INV}${RESET}"
}

print_section() {
    echo -e "${BRIGHT_CYAN}${BOX_VERTICAL}${RESET}  ${BOLD}${BRIGHT_YELLOW}$1${RESET}"
}

print_line() {
    echo -e "${BRIGHT_CYAN}${BOX_VERTICAL}${RESET}  $1"
}

print_success() {
    echo -e "${BRIGHT_CYAN}${BOX_VERTICAL}${RESET}  ${BRIGHT_GREEN}✓${RESET} $1"
}

print_info() {
    echo -e "${BRIGHT_CYAN}${BOX_VERTICAL}${RESET}  ${BRIGHT_BLUE}ℹ${RESET} $1"
}

print_footer() {
    echo -e "${BRIGHT_CYAN}${BOX_BOTTOM}${RESET}"
    echo ""
}

# Animated progress bar
progress_bar() {
    local width=40
    local progress=$1
    local filled=$((width * progress / 100))
    local empty=$((width - filled))
    
    printf "\r${BRIGHT_CYAN}${BOX_VERTICAL}${RESET}  ["
    printf "${BRIGHT_GREEN}"
    for ((i=0; i<filled; i++)); do printf "█"; done
    printf "${RESET}"
    for ((i=0; i<empty; i++)); do printf "░"; done
    printf "] %3d%%" "$progress"
}

# Animated checkmark
animate_check() {
    local msg="$1"
    for i in {1..3}; do
        printf "\r${BRIGHT_CYAN}${BOX_VERTICAL}${RESET}  ${BRIGHT_YELLOW}⋯${RESET} %s" "$msg"
        sleep 0.1
    done
    printf "\r${BRIGHT_CYAN}${BOX_VERTICAL}${RESET}  ${BRIGHT_GREEN}✓${RESET} %s\n" "$msg"
}

# Determine real home directory
detect_home() {
    local real_home="${HOME}"
    if [ -n "${SUDO_USER:-}" ]; then
        if command -v getent >/dev/null 2>&1; then
            real_home="$(getent passwd "$SUDO_USER" | cut -d: -f6)"
        elif [ "$(uname -s)" = "Darwin" ]; then
            real_home="$(dscl . -read /Users/"$SUDO_USER" NFSHomeDirectory 2>/dev/null | awk '{print $2}')"
        elif [ -r /etc/passwd ]; then
            real_home="$(awk -F: -v user="$SUDO_USER" '$1 == user { print $6; exit }' /etc/passwd)"
        fi
    fi
    echo "$real_home"
}

# Main installation logic
main() {
    print_header
    
    # Check for macOS protected directories
    local os_name
    os_name="$(uname -s)"
    case "$os_name:$INSTALL_DIR" in
        Darwin:/bin|Darwin:/sbin|Darwin:/usr/bin|Darwin:/usr/sbin|Darwin:/System|Darwin:/System/*)
            print_line "${BRIGHT_RED}Error: Cannot install into protected macOS directory${RESET}"
            print_line "Use /usr/local/bin or a user-writable directory instead"
            print_footer
            exit 1
            ;;
    esac
    
    # Strip trailing slash
    INSTALL_DIR="${INSTALL_DIR%/}"
    
    local real_home
    real_home="$(detect_home)"
    [ -z "$real_home" ] && real_home="${HOME}"
    
    # Show version info
    print_section "Version Information"
    print_line "  ${DIM}Component:${RESET}  ${BOLD}SwarmOS TUI${RESET}"
    print_line "  ${DIM}Version:${RESET}    ${BRIGHT_CYAN}${VERSION}${RESET}"
    print_line "  ${DIM}Build ID:${RESET}   ${BRIGHT_MAGENTA}${BUILD_ID}${RESET}"
    print_line "  ${DIM}Platform:${RESET}   ${os_name}"
    echo ""
    
    # Installation locations
    print_section "Installation Targets"
    print_line "  ${DIM}System:${RESET}   ${BRIGHT_BLUE}${INSTALL_DIR}/${RESET}"
    if [ -d "${real_home}/bin" ]; then
        print_line "  ${DIM}User:${RESET}     ${BRIGHT_BLUE}${real_home}/bin/${RESET}"
    fi
    if [ -d "${real_home}/.local/bin" ]; then
        print_line "  ${DIM}XDG:${RESET}      ${BRIGHT_BLUE}${real_home}/.local/bin/${RESET}"
    fi
    echo ""
    
    # Install binaries
    print_section "Installing Binaries"
    
    # Check if we need sudo
    local use_sudo=false
    if [ ! -w "$INSTALL_DIR" ]; then
        if ! command -v sudo >/dev/null 2>&1; then
            print_line "${BRIGHT_RED}Error: sudo required to install to ${INSTALL_DIR}${RESET}"
            print_footer
            exit 1
        fi
        use_sudo=true
        print_info "Elevated privileges required"
    fi
    
    # Animate installation
    local binaries=("$BINARY_NAME" "$HEADLESS_BINARY")
    local total=${#binaries[@]}
    local current=0
    
    for binary in "${binaries[@]}"; do
        current=$((current + 1))
        local pct=$((current * 100 / total))
        
        # Show progress
        printf "${BRIGHT_CYAN}${BOX_VERTICAL}${RESET}  "
        spinner
        printf " Installing ${BOLD}%s${RESET}...\n" "$binary"
        
        # Do the install
        if [ "$use_sudo" = true ]; then
            sudo install -m 0755 "$binary" "${INSTALL_DIR}/${binary}"
        else
            install -m 0755 "$binary" "${INSTALL_DIR}/${binary}"
        fi
        
        clear_spinner
        animate_check "${binary} → ${INSTALL_DIR}/"
    done
    
    # Progress bar animation to 100%
    for i in 50 60 70 80 90 100; do
        progress_bar $i
        sleep 0.05
    done
    echo ""
    echo ""
    
    # Install to user bin if it exists
    if [ -d "${real_home}/bin" ]; then
        print_section "User Local Installation"
        for binary in "${binaries[@]}"; do
            printf "${BRIGHT_CYAN}${BOX_VERTICAL}${RESET}  "
            spinner
            printf " Installing ${BOLD}%s${RESET} to ~/bin...\n" "$binary"
            install -m 0755 "$binary" "${real_home}/bin/${binary}"
            clear_spinner
            animate_check "${binary} → ~/bin/"
        done
        echo ""
    fi

    # Install to ~/.local/bin if it exists — XDG path, typically earlier in
    # PATH than /usr/local/bin, so a stale copy here will shadow the system
    # install and `swarm -version` will still show the old build.
    if [ -d "${real_home}/.local/bin" ]; then
        print_section "XDG Local Installation"
        for binary in "${binaries[@]}"; do
            printf "${BRIGHT_CYAN}${BOX_VERTICAL}${RESET}  "
            spinner
            printf " Installing ${BOLD}%s${RESET} to ~/.local/bin...\n" "$binary"
            install -m 0755 "$binary" "${real_home}/.local/bin/${binary}"
            clear_spinner
            animate_check "${binary} → ~/.local/bin/"
        done
        echo ""
    fi
    
    # Verify installation
    print_section "Verification"
    for binary in "${binaries[@]}"; do
        local full_path="${INSTALL_DIR}/${binary}"
        if [ -x "$full_path" ]; then
            local size
            size="$(du -h "$full_path" | cut -f1)"
            print_success "${binary} installed (${size})"
        else
            print_line "${BRIGHT_RED}✗ ${binary} not found${RESET}"
        fi
    done
    echo ""
    
    # Show version output
    print_section "Installed Version"
    local version_output
    version_output="$($INSTALL_DIR/$BINARY_NAME -version 2>/dev/null | head -3 || true)"
    while IFS= read -r line; do
        print_line "  ${DIM}${line}${RESET}"
    done <<< "$version_output"
    
    print_footer
    
    # Summary box
    echo -e "${BRIGHT_GREEN}${BOX_TOP}${RESET}"
    echo -e "${BRIGHT_GREEN}${BOX_VERTICAL}${RESET}  ${BOLD}${BRIGHT_WHITE}Installation Complete!${RESET}                                      ${BRIGHT_GREEN}${BOX_VERTICAL}${RESET}"
    echo -e "${BRIGHT_GREEN}${BOX_T}─────────────────────────────────────────────────────────────────${BOX_T_INV}${RESET}"
    echo -e "${BRIGHT_GREEN}${BOX_VERTICAL}${RESET}  ${BRIGHT_CYAN}${BINARY_NAME}${RESET}      ${BRIGHT_GREEN}✓ Ready${RESET}                                    ${BRIGHT_GREEN}${BOX_VERTICAL}${RESET}"
    echo -e "${BRIGHT_GREEN}${BOX_VERTICAL}${RESET}  ${BRIGHT_CYAN}${HEADLESS_BINARY}${RESET}  ${BRIGHT_GREEN}✓ Ready${RESET}                                    ${BRIGHT_GREEN}${BOX_VERTICAL}${RESET}"
    echo -e "${BRIGHT_GREEN}${BOX_BOTTOM}${RESET}"
    echo ""
}

main "$@"
