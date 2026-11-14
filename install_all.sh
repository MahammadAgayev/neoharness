#!/bin/sh
# Builds neoharness and the macOS app, installs both: the binary to $BIN
# (default ~/.local/bin), the app to $APPS (default ~/Applications), and
# restarts the hub on the new binary.
set -eu

APPS=${APPS:-$HOME/Applications}
cd "$(dirname "$0")"

if [ "$(uname)" != Darwin ]; then
	echo "install_all.sh is for macOS; ./install.sh installs the TUI" >&2
	exit 1
fi
if ! command -v npm >/dev/null 2>&1; then
	echo "the app needs Node.js and npm: brew install node" >&2
	exit 1
fi
# The hub binary is also called neoharness, so match the app's own path.
if pgrep -f 'neoharness\.app/Contents/MacOS/neoharness' >/dev/null 2>&1; then
	echo "quit the neoharness app first (⌘Q); the hub and its turns go on" >&2
	exit 1
fi

trap '[ $? -eq 0 ] || echo "install_all.sh: the step above failed; nothing after it ran" >&2' EXIT

./install.sh

cd desktop
npm ci --no-audit --no-fund
npm run dist
app=$(ls -d dist/neoharness-darwin-*/neoharness.app | head -n 1)
mkdir -p "$APPS"
rm -rf "$APPS/neoharness.app"
ditto "$app" "$APPS/neoharness.app"
# Spotlight can take a long time to find a new copy on its own.
mdimport "$APPS/neoharness.app" 2>/dev/null || true
echo "installed $APPS/neoharness.app"
echo "restarting the hub on the new binary; this stops the running turns"
"${BIN:-$HOME/.local/bin}/neoharness" hub restart
echo "to open the app:"
echo "  open \"$APPS/neoharness.app\""
