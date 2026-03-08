#!/bin/bash

# Build script for yagent

set -e

echo "Building yagent..."

# Build the application
go build -o yagent .

echo "Build complete! Use './yagent' to run the application."