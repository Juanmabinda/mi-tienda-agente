#!/usr/bin/env bash
# Build a signed + notarized .app bundle for Mac.
#
# Usage: ./scripts/build-mac-app.sh [version]

set -euo pipefail

VERSION="${1:-0.2.2}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
DIST_DIR="$ROOT_DIR/dist"
APP_NAME="Mi Tienda Print"
APP_BUNDLE="$DIST_DIR/$APP_NAME.app"
APP_ZIP="$DIST_DIR/mi-tienda-print-mac.zip"

# Load credentials
set -a
source "$ROOT_DIR/.env"
set +a

cd "$ROOT_DIR"

echo "==> Cleaning dist..."
rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

echo "==> Building binary..."
go build -o "$DIST_DIR/mi-tienda-print" .

echo "==> Creating .app bundle..."
mkdir -p "$APP_BUNDLE/Contents/MacOS"
mkdir -p "$APP_BUNDLE/Contents/Resources"

# Move binary into bundle
mv "$DIST_DIR/mi-tienda-print" "$APP_BUNDLE/Contents/MacOS/mi-tienda-print"

# Launcher script (opens Terminal with the binary)
cat > "$APP_BUNDLE/Contents/MacOS/launcher" <<'EOF'
#!/usr/bin/env bash
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
osascript -e "tell application \"Terminal\"
  activate
  do script \"clear && '$DIR/mi-tienda-print'\"
end tell"
EOF
chmod +x "$APP_BUNDLE/Contents/MacOS/launcher"

# Info.plist
cat > "$APP_BUNDLE/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>launcher</string>
    <key>CFBundleIdentifier</key>
    <string>app.mitienda.print</string>
    <key>CFBundleName</key>
    <string>Mi Tienda Print</string>
    <key>CFBundleDisplayName</key>
    <string>Mi Tienda Print</string>
    <key>CFBundleVersion</key>
    <string>$VERSION</string>
    <key>CFBundleShortVersionString</key>
    <string>$VERSION</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleSignature</key>
    <string>????</string>
    <key>LSMinimumSystemVersion</key>
    <string>11.0</string>
    <key>NSHighResolutionCapable</key>
    <true/>
</dict>
</plist>
EOF

echo "==> Signing the binary inside the app..."
codesign --sign "$APPLE_DEVELOPER_ID" --options runtime --timestamp --force \
  "$APP_BUNDLE/Contents/MacOS/mi-tienda-print"

echo "==> Signing the launcher..."
codesign --sign "$APPLE_DEVELOPER_ID" --options runtime --timestamp --force \
  "$APP_BUNDLE/Contents/MacOS/launcher"

echo "==> Signing the .app bundle (deep)..."
codesign --sign "$APPLE_DEVELOPER_ID" --options runtime --timestamp --force --deep \
  "$APP_BUNDLE"

echo "==> Verifying..."
codesign --verify --verbose "$APP_BUNDLE"

echo "==> Zipping for notarization..."
ditto -c -k --keepParent "$APP_BUNDLE" "$APP_ZIP"

echo "==> Submitting to Apple notary service..."
xcrun notarytool submit "$APP_ZIP" \
  --apple-id "$APPLE_ID" \
  --password "$APPLE_APP_PASSWORD" \
  --team-id "$APPLE_TEAM_ID" \
  --wait

echo "==> Stapling notarization ticket..."
xcrun stapler staple "$APP_BUNDLE"

echo "==> Re-zipping with stapled ticket..."
rm "$APP_ZIP"
ditto -c -k --keepParent "$APP_BUNDLE" "$APP_ZIP"

echo ""
echo "Done. App bundle: $APP_BUNDLE"
echo "Distributable zip: $APP_ZIP"
