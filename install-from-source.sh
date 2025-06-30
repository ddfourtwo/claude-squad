#!/usr/bin/env bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Default values
INSTALL_NAME="cs"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"

# Print colored output
print_success() {
    echo -e "${GREEN}✓${NC} $1"
}

print_error() {
    echo -e "${RED}✗${NC} $1"
}

print_info() {
    echo -e "${YELLOW}→${NC} $1"
}

# Parse command line arguments
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --name)
                INSTALL_NAME="$2"
                shift 2
                ;;
            --bin-dir)
                BIN_DIR="$2"
                shift 2
                ;;
            -h|--help)
                echo "Usage: $0 [OPTIONS]"
                echo ""
                echo "Options:"
                echo "  --name <name>      Binary name (default: cs)"
                echo "  --bin-dir <dir>    Installation directory (default: ~/.local/bin)"
                echo "  -h, --help         Show this help message"
                exit 0
                ;;
            *)
                print_error "Unknown option: $1"
                echo "Use -h or --help for usage information"
                exit 1
                ;;
        esac
    done
}

# Check if a command exists
check_command() {
    if command -v "$1" &> /dev/null; then
        return 0
    else
        return 1
    fi
}

# Check Go installation
check_go() {
    print_info "Checking Go installation..."
    
    if ! check_command go; then
        print_error "Go is not installed. Please install Go 1.23 or later."
        echo "Visit https://golang.org/dl/ for installation instructions."
        exit 1
    fi
    
    GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
    print_success "Go $GO_VERSION is installed"
    
    # Check Go version (requires at least 1.23)
    REQUIRED_GO_VERSION="1.23"
    if ! printf '%s\n' "$REQUIRED_GO_VERSION" "$GO_VERSION" | sort -V | head -n1 | grep -q "$REQUIRED_GO_VERSION"; then
        print_error "Go version $GO_VERSION is too old. Please install Go $REQUIRED_GO_VERSION or later."
        exit 1
    fi
}

# Check required dependencies
check_dependencies() {
    print_info "Checking required dependencies..."
    
    local missing_deps=()
    
    # Check tmux
    if ! check_command tmux; then
        missing_deps+=("tmux")
    else
        print_success "tmux is installed"
    fi
    
    # Check GitHub CLI
    if ! check_command gh; then
        missing_deps+=("gh (GitHub CLI)")
    else
        print_success "gh (GitHub CLI) is installed"
    fi
    
    # Report missing dependencies
    if [ ${#missing_deps[@]} -gt 0 ]; then
        print_error "Missing required dependencies:"
        for dep in "${missing_deps[@]}"; do
            echo "  - $dep"
        done
        echo ""
        echo "Please install the missing dependencies:"
        echo "  - tmux: https://github.com/tmux/tmux/wiki/Installing"
        echo "  - gh: https://cli.github.com/"
        exit 1
    fi
}

# Build the binary
build_binary() {
    print_info "Building Claude Squad from source..."
    
    # Get the directory of this script
    SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
    
    # Change to the project directory
    cd "$SCRIPT_DIR"
    
    # Clean any previous builds
    rm -f claude-squad
    
    # Build the binary
    if ! go build -o claude-squad .; then
        print_error "Failed to build Claude Squad"
        exit 1
    fi
    
    print_success "Successfully built Claude Squad"
}

# Install the binary
install_binary() {
    print_info "Installing binary to $BIN_DIR/$INSTALL_NAME..."
    
    # Create bin directory if it doesn't exist
    if [ ! -d "$BIN_DIR" ]; then
        mkdir -p "$BIN_DIR"
    fi
    
    # Remove existing binary if present
    if [ -f "$BIN_DIR/$INSTALL_NAME" ]; then
        print_info "Removing existing installation..."
        rm -f "$BIN_DIR/$INSTALL_NAME"
    fi
    
    # Copy the binary
    cp claude-squad "$BIN_DIR/$INSTALL_NAME"
    chmod +x "$BIN_DIR/$INSTALL_NAME"
    
    # Clean up build artifact
    rm -f claude-squad
    
    print_success "Installed to $BIN_DIR/$INSTALL_NAME"
}

# Setup shell PATH
setup_path() {
    # Check if bin directory is already in PATH
    if [[ ":$PATH:" == *":${BIN_DIR}:"* ]]; then
        print_success "Directory $BIN_DIR is already in PATH"
        return
    fi
    
    print_info "Adding $BIN_DIR to PATH..."
    
    # Detect shell and update appropriate config file
    case $SHELL in
        */zsh)
            PROFILE=$HOME/.zshrc
            ;;
        */bash)
            PROFILE=$HOME/.bashrc
            ;;
        */fish)
            PROFILE=$HOME/.config/fish/config.fish
            ;;
        */ash)
            PROFILE=$HOME/.profile
            ;;
        *)
            print_error "Could not detect shell, manually add $BIN_DIR to your PATH."
            return
            ;;
    esac
    
    # Add to PATH
    echo >> "$PROFILE"
    echo "# Added by Claude Squad installer" >> "$PROFILE"
    echo "export PATH=\"\$PATH:$BIN_DIR\"" >> "$PROFILE"
    
    print_success "Added $BIN_DIR to PATH in $PROFILE"
    echo "Please run: source $PROFILE"
    echo "Or restart your terminal for PATH changes to take effect"
}

# Verify installation
verify_installation() {
    print_info "Verifying installation..."
    
    # Check if binary exists and is executable
    if [ ! -x "$BIN_DIR/$INSTALL_NAME" ]; then
        print_error "Installation verification failed: binary not found or not executable"
        exit 1
    fi
    
    # Try to run version command
    if OUTPUT=$("$BIN_DIR/$INSTALL_NAME" version 2>&1); then
        print_success "Installation verified:"
        echo "  $OUTPUT"
    else
        print_error "Installation verification failed: could not run version command"
        echo "  Error: $OUTPUT"
        exit 1
    fi
}

# Main installation flow
main() {
    echo "Claude Squad - Install from Source"
    echo "=================================="
    echo ""
    
    parse_args "$@"
    
    # Run all checks and installation steps
    check_go
    check_dependencies
    build_binary
    install_binary
    setup_path
    verify_installation
    
    echo ""
    print_success "Installation complete!"
    echo ""
    echo "To start using Claude Squad, run:"
    echo "  $INSTALL_NAME"
    echo ""
    
    # Reminder about PATH if needed
    if [[ ":$PATH:" != *":${BIN_DIR}:"* ]]; then
        echo "Note: You may need to restart your terminal or run 'source' on your shell"
        echo "configuration file for the PATH changes to take effect."
    fi
}

# Run main function
main "$@"