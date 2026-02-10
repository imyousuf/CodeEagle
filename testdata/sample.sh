#!/bin/bash
# Sample shell script for testing

export APP_NAME="myapp"
export APP_VERSION="1.0.0"

BASE_DIR="/opt/app"
LOG_LEVEL=info
readonly MAX_RETRIES=3

declare -x EXPORTED_SETTING="enabled"

# Greet a user
function greet() {
    local name="$1"
    echo "Hello, ${name}!"
}

# Cleanup temp files
cleanup() {
    rm -rf /tmp/work-*
    echo "Cleaned up"
}

start_server() {
    echo "Starting server on port ${PORT:-8080}"
}

source ./lib/helpers.sh
. ./lib/utils.sh

greet "World"
cleanup
