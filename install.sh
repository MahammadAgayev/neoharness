#!/bin/sh
# Builds neoharness and installs it to $BIN (default ~/.local/bin).
set -eu

BIN=${BIN:-$HOME/.local/bin}
cd "$(dirname "$0")"
mkdir -p "$BIN"
go build -o "$BIN/neoharness" ./cmd/neoharness
echo "installed $BIN/neoharness"
