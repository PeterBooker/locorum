#!/usr/bin/env bash
#
# Install Gio's required Linux build dependencies.
#
# Shared between every CI workflow that runs `go build`/`go test` against
# packages that compile the Gio backend (i.e. anything under internal/ui or
# main.go). When the dep list churns, fix it here once.

set -euo pipefail

if ! command -v apt-get >/dev/null 2>&1; then
    echo "ERROR: apt-get not found; this script targets Ubuntu/Debian runners" >&2
    exit 1
fi

sudo apt-get update
sudo apt-get install -y --no-install-recommends \
    gcc pkg-config \
    libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev \
    libgles2-mesa-dev libegl1-mesa-dev libffi-dev \
    libxcursor-dev libvulkan-dev \
    libgtk-3-dev
