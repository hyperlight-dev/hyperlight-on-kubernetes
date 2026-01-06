#!/bin/bash
#
# Common utilities for Hyperlight deploy scripts
#
# Source this file in other scripts:
#   SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   source "${SCRIPT_DIR}/../common.sh"
#   # or for scripts in deploy/ directly:
#   source "${SCRIPT_DIR}/common.sh"
#

# =============================================================================
# Colours
# =============================================================================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'  # No Colour

# =============================================================================
# Logging functions
# =============================================================================
log_info()    { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
log_warning() { echo -e "${YELLOW}[WARNING]${NC} $1"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $1"; }
log_debug()   { [ "${DEBUG:-}" = "1" ] && echo -e "${CYAN}[DEBUG]${NC} $1"; }
log_step()    { echo -e "${BOLD}==>${NC} $1"; }

# =============================================================================
# Utility functions
# =============================================================================

# Check if a command exists
require_cmd() {
    local cmd="$1"
    local install_hint="${2:-}"
    
    if ! command -v "$cmd" &> /dev/null; then
        log_error "$cmd is not installed"
        [ -n "$install_hint" ] && log_info "Install: $install_hint"
        return 1
    fi
    return 0
}

# Check multiple commands exist
require_cmds() {
    local missing=()
    for cmd in "$@"; do
        command -v "$cmd" &> /dev/null || missing+=("$cmd")
    done
    
    if [ ${#missing[@]} -gt 0 ]; then
        log_error "Missing required commands: ${missing[*]}"
        return 1
    fi
    return 0
}

# Confirm action with user
confirm() {
    local prompt="${1:-Continue?}"
    local default="${2:-no}"
    
    if [ "$default" = "yes" ]; then
        read -p "$prompt (Y/n): " answer
        [ -z "$answer" ] || [[ "$answer" =~ ^[Yy] ]]
    else
        read -p "$prompt (y/N): " answer
        [[ "$answer" =~ ^[Yy] ]]
    fi
}

# Wait for a condition with timeout
wait_for() {
    local description="$1"
    local check_cmd="$2"
    local timeout="${3:-60}"
    local interval="${4:-5}"
    
    log_info "Waiting for $description..."
    local elapsed=0
    while ! eval "$check_cmd" &>/dev/null; do
        if [ $elapsed -ge $timeout ]; then
            log_error "Timeout waiting for $description"
            return 1
        fi
        sleep $interval
        elapsed=$((elapsed + interval))
    done
    log_success "$description ready"
    return 0
}
