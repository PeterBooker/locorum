#!/usr/bin/env bash
# packaging/macos/make-dmg.sh APP_PATH OUTPUT_DMG VERSION
#
# Wraps create-dmg with Locorum's drag-to-Applications layout.
# Requires `brew install create-dmg`. macOS only.

set -euo pipefail

APP_PATH="${1:?usage: $0 APP_PATH OUTPUT_DMG VERSION}"
OUTPUT_DMG="${2:?usage: $0 APP_PATH OUTPUT_DMG VERSION}"
VERSION="${3:?usage: $0 APP_PATH OUTPUT_DMG VERSION}"

APP_BASENAME="$(basename "$APP_PATH")"

# create-dmg refuses to overwrite an existing image.
rm -f "$OUTPUT_DMG"

create-dmg \
  --volname "Locorum $VERSION" \
  --window-size 500 400 \
  --icon-size 100 \
  --icon "$APP_BASENAME" 120 180 \
  --hide-extension "$APP_BASENAME" \
  --app-drop-link 380 180 \
  --no-internet-enable \
  "$OUTPUT_DMG" \
  "$APP_PATH"
