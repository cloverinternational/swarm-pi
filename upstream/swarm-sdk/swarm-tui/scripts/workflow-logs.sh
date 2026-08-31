#!/bin/bash
# Workflow Log Viewer
# View and analyze workflow execution logs

set -e

LOGS_DIR=".swarm/workflow-logs"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

function show_help() {
    cat << EOF
Workflow Log Viewer

Usage: $0 [command] [options]

Commands:
    list        List all workflow runs
    latest      Show the latest workflow run
    show <id>   Show a specific workflow run
    config <id> Show workflow configuration
    logs <id>   Show execution logs
    groups <id> Show group logs
    agents <id> Show agent logs  
    errors <id> Show only errors
    tail        Tail the latest log file

Options:
    -h, --help  Show this help message

Examples:
    $0 list
    $0 latest
    $0 show git_staging_workflow_20240205-143000
    $0 logs git_staging_workflow_20240205-143000
    $0 tail

EOF
}

function list_runs() {
    echo -e "${CYAN}Workflow Execution Runs:${NC}\n"
    
    if [ ! -d "$LOGS_DIR" ]; then
        echo -e "${YELLOW}No workflow logs found${NC}"
        echo "Logs directory: $LOGS_DIR"
        return
    fi
    
    local count=0
    for run_dir in "$LOGS_DIR"/*/ ; do
        if [ -d "$run_dir" ] && [ "$(basename "$run_dir")" != "latest" ]; then
            count=$((count + 1))
            local run_id=$(basename "$run_dir")
            local config_file="$run_dir/config.yaml"
            
            if [ -f "$config_file" ]; then
                local workflow_name=$(grep "workflow_name:" "$config_file" | cut -d: -f2- | xargs)
                local status=$(grep "^status:" "$config_file" | cut -d: -f2- | xargs)
                local start_time=$(grep "start_time:" "$config_file" | cut -d: -f2- | xargs)
                local duration=$(grep "^duration:" "$config_file" | cut -d: -f2- | xargs || echo "N/A")
                
                # Color status
                case "$status" in
                    "completed") status="${GREEN}✓ $status${NC}" ;;
                    "failed") status="${RED}✗ $status${NC}" ;;
                    "running") status="${YELLOW}⋯ $status${NC}" ;;
                    *) status="${BLUE}$status${NC}" ;;
                esac
                
                echo -e "${BLUE}$count.${NC} $run_id"
                echo -e "   Workflow: ${GREEN}$workflow_name${NC}"
                echo -e "   Status: $status"
                echo -e "   Started: $start_time"
                [ "$duration" != "N/A" ] && echo -e "   Duration: $duration"
                echo ""
            fi
        fi
    done
    
    if [ $count -eq 0 ]; then
        echo -e "${YELLOW}No workflow runs found${NC}"
    else
        echo -e "${CYAN}Total runs: $count${NC}"
    fi
}

function show_latest() {
    if [ ! -L "$LOGS_DIR/latest" ]; then
        echo -e "${RED}No workflow runs found${NC}"
        return 1
    fi
    
    local latest_dir=$(readlink -f "$LOGS_DIR/latest")
    local run_id=$(basename "$latest_dir")
    
    echo -e "${CYAN}Latest Workflow Run: ${GREEN}$run_id${NC}\n"
    show_run "$run_id"
}

function show_run() {
    local run_id="$1"
    local run_dir="$LOGS_DIR/$run_id"
    
    if [ ! -d "$run_dir" ]; then
        echo -e "${RED}Run not found: $run_id${NC}"
        return 1
    fi
    
    local config_file="$run_dir/config.yaml"
    
    if [ ! -f "$config_file" ]; then
        echo -e "${RED}Config file not found${NC}"
        return 1
    fi
    
    echo -e "${CYAN}=== Workflow Execution ===${NC}\n"
    
    cat "$config_file" | grep -E "^(workflow_|start_|end_|duration:|status:|provider:|model:|input:)" | while read -r line; do
        local key=$(echo "$line" | cut -d: -f1)
        local value=$(echo "$line" | cut -d: -f2- | xargs)
        
        case "$key" in
            "status")
                case "$value" in
                    "completed") value="${GREEN}✓ $value${NC}" ;;
                    "failed") value="${RED}✗ $value${NC}" ;;
                    "running") value="${YELLOW}⋯ $value${NC}" ;;
                esac
                ;;
        esac
        
        printf "${BLUE}%-20s${NC} %s\n" "$key:" "$value"
    done
    
    echo ""
    echo -e "${CYAN}=== Groups ===${NC}\n"
    
    if [ -d "$run_dir/groups" ]; then
        for group_file in "$run_dir/groups"/*.json; do
            if [ -f "$group_file" ]; then
                local group_name=$(basename "$group_file" .json)
                echo -e "  ${GREEN}▸${NC} $group_name"
            fi
        done
    fi
    
    echo ""
    echo -e "${CYAN}=== Agents ===${NC}\n"
    
    if [ -d "$run_dir/agents" ]; then
        for agent_file in "$run_dir/agents"/*.json; do
            if [ -f "$agent_file" ]; then
                local agent_name=$(basename "$agent_file" .json)
                echo -e "  ${BLUE}▸${NC} $agent_name"
            fi
        done
    fi
    
    echo ""
    echo -e "${CYAN}Path:${NC} $run_dir"
}

function show_config() {
    local run_id="$1"
    local config_file="$LOGS_DIR/$run_id/config.yaml"
    
    if [ ! -f "$config_file" ]; then
        echo -e "${RED}Config not found for run: $run_id${NC}"
        return 1
    fi
    
    echo -e "${CYAN}Configuration for: $run_id${NC}\n"
    cat "$config_file"
}

function show_logs() {
    local run_id="$1"
    local log_file="$LOGS_DIR/$run_id/execution.log"
    
    if [ ! -f "$log_file" ]; then
        echo -e "${RED}Log file not found for run: $run_id${NC}"
        return 1
    fi
    
    echo -e "${CYAN}Execution logs for: $run_id${NC}\n"
    
    cat "$log_file" | while IFS= read -r line; do
        local level=$(echo "$line" | jq -r '.level' 2>/dev/null || echo "")
        local message=$(echo "$line" | jq -r '.message' 2>/dev/null || echo "$line")
        local timestamp=$(echo "$line" | jq -r '.timestamp' 2>/dev/null || echo "")
        
        case "$level" in
            "ERROR") echo -e "${RED}[$timestamp] ERROR:${NC} $message" ;;
            "WARN") echo -e "${YELLOW}[$timestamp] WARN:${NC} $message" ;;
            "INFO") echo -e "${GREEN}[$timestamp] INFO:${NC} $message" ;;
            "DEBUG") echo -e "${BLUE}[$timestamp] DEBUG:${NC} $message" ;;
            *) echo "$line" ;;
        esac
    done
}

function show_errors() {
    local run_id="$1"
    local log_file="$LOGS_DIR/$run_id/execution.log"
    
    if [ ! -f "$log_file" ]; then
        echo -e "${RED}Log file not found for run: $run_id${NC}"
        return 1
    fi
    
    echo -e "${CYAN}Errors for: $run_id${NC}\n"
    
    cat "$log_file" | grep '"level":"ERROR"' | while IFS= read -r line; do
        local message=$(echo "$line" | jq -r '.message' 2>/dev/null)
        local error=$(echo "$line" | jq -r '.error' 2>/dev/null)
        local timestamp=$(echo "$line" | jq -r '.timestamp' 2>/dev/null)
        
        echo -e "${RED}[$timestamp]${NC}"
        echo -e "  Message: $message"
        [ "$error" != "null" ] && echo -e "  Error: $error"
        echo ""
    done
}

function tail_latest() {
    if [ ! -L "$LOGS_DIR/latest" ]; then
        echo -e "${RED}No workflow runs found${NC}"
        return 1
    fi
    
    local latest_dir=$(readlink -f "$LOGS_DIR/latest")
    local log_file="$latest_dir/execution.log"
    
    if [ ! -f "$log_file" ]; then
        echo -e "${RED}Log file not found${NC}"
        return 1
    fi
    
    echo -e "${CYAN}Tailing latest log: $log_file${NC}\n"
    tail -f "$log_file" | while IFS= read -r line; do
        local level=$(echo "$line" | jq -r '.level' 2>/dev/null || echo "")
        local message=$(echo "$line" | jq -r '.message' 2>/dev/null || echo "$line")
        local timestamp=$(echo "$line" | jq -r '.timestamp' 2>/dev/null || echo "")
        
        case "$level" in
            "ERROR") echo -e "${RED}[$timestamp] ERROR:${NC} $message" ;;
            "WARN") echo -e "${YELLOW}[$timestamp] WARN:${NC} $message" ;;
            "INFO") echo -e "${GREEN}[$timestamp] INFO:${NC} $message" ;;
            "DEBUG") echo -e "${BLUE}[$timestamp] DEBUG:${NC} $message" ;;
            *) echo "$line" ;;
        esac
    done
}

# Main command handling
case "${1:-list}" in
    list) list_runs ;;
    latest) show_latest ;;
    show) show_run "$2" ;;
    config) show_config "$2" ;;
    logs) show_logs "$2" ;;
    errors) show_errors "$2" ;;
    tail) tail_latest ;;
    -h|--help) show_help ;;
    *) 
        echo -e "${RED}Unknown command: $1${NC}"
        show_help
        exit 1
        ;;
esac
