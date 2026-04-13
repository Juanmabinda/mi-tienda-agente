#!/usr/bin/env bash
# Sign and notarize a Mac binary using credentials from .env
#
# Usage: ./scripts/sign-and-notarize.sh mi-tienda-print-mac-arm64

set -euo pipefail

if [ -z "${1:-}" ]; then
  echo "Usage: $0 <binary-path>"
  exit 1
fi

BINARY="$1"

if [ ! -f "$BINARY" ]; then
  echo "Error: $BINARY not found"
  exit 1
fi

# Load credentials
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

if [ ! -f "$ROOT_DIR/.env" ]; then
  echo "Error: .env not found in $ROOT_DIR"
  echo "Copy .env.example and fill in your credentials"
  exit 1
fi

set -a
source "$ROOT_DIR/.env"
set +a

if [ -z "${APPLE_ID:-}" ] || [ -z "${APPLE_TEAM_ID:-}" ] || [ -z "${APPLE_APP_PASSWORD:-}" ] || [ -z "${APPLE_DEVELOPER_ID:-}" ]; then
  echo "Error: missing credentials in .env (APPLE_ID, APPLE_TEAM_ID, APPLE_APP_PASSWORD, APPLE_DEVELOPER_ID)"
  exit 1
fi

echo "==> Signing $BINARY..."
codesign --sign "$APPLE_DEVELOPER_ID" --options runtime --timestamp --force "$BINARY"

echo "==> Verifying signature..."
codesign --verify --verbose "$BINARY"

echo "==> Zipping for notarization..."
ZIP="${BINARY}.zip"
rm -f "$ZIP"
ditto -c -k --keepParent "$BINARY" "$ZIP"

echo "==> Submitting to Apple notary service (this can take a few minutes)..."
xcrun notarytool submit "$ZIP" \
  --apple-id "$APPLE_ID" \
  --password "$APPLE_APP_PASSWORD" \
  --team-id "$APPLE_TEAM_ID" \
  --wait

echo "==> Cleanup zip..."
rm -f "$ZIP"

echo ""
echo "Done. $BINARY is signed and notarized."
echo "Note: standalone binaries cannot be stapled, but Apple verifies them online on first run."
