APP_NAME := locorum
APP_ID   := com.peterbooker.locorum
BUILD_DIR := build/bin
DIST_DIR  := build/dist
ICON_DIR := assets/icons
VERSION_PKG := github.com/PeterBooker/locorum/internal/version

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# 4-part Major.Minor.Patch.Build for gogio and WiX. Derived from VERSION when
# VERSION matches "[v]X.Y.Z..."; falls back to 0.0.0.0 for dev / non-tagged
# builds (gogio and WiX both require strict numeric form).
SEMVER  ?= $(shell echo "$(VERSION)" | sed -nE 's/^v?([0-9]+\.[0-9]+\.[0-9]+).*/\1.0/p' | grep . || echo 0.0.0.0)

LDFLAGS_VERSION := -X $(VERSION_PKG).Version=$(VERSION) -X $(VERSION_PKG).Commit=$(COMMIT) -X $(VERSION_PKG).Date=$(DATE)
LDFLAGS_DEV     := $(LDFLAGS_VERSION)
LDFLAGS_RELEASE := -s -w $(LDFLAGS_VERSION)

ICON_SIZES := 16 32 48 64 128 256 512 1024
ICON_PNGS  := $(foreach s,$(ICON_SIZES),$(ICON_DIR)/icon-$(s).png)

GOGIO ?= gogio
WIX   ?= wix

.PHONY: build linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64 windows-arm64 all clean test icons \
        dist-windows dist-windows-amd64 dist-windows-arm64 \
        msi-windows-amd64 msi-windows-arm64 \
        dist-linux dist-linux-amd64 dist-linux-arm64 \
        tarball-linux-amd64 tarball-linux-arm64 \
        dist-macos dist-macos-app dmg-macos

build:
	go build -ldflags "$(LDFLAGS_DEV)" -o $(BUILD_DIR)/$(APP_NAME) .

linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS_RELEASE)" -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64 .

linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS_RELEASE)" -o $(BUILD_DIR)/$(APP_NAME)-linux-arm64 .

darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS_RELEASE)" -o $(BUILD_DIR)/$(APP_NAME)-darwin-amd64 .

darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS_RELEASE)" -o $(BUILD_DIR)/$(APP_NAME)-darwin-arm64 .

windows-amd64:
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS_RELEASE)" -o $(BUILD_DIR)/$(APP_NAME)-windows-amd64.exe .

windows-arm64:
	GOOS=windows GOARCH=arm64 go build -ldflags "$(LDFLAGS_RELEASE)" -o $(BUILD_DIR)/$(APP_NAME)-windows-arm64.exe .

all: linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64 windows-arm64

icons: $(ICON_PNGS) appicon.png

appicon.png: $(ICON_DIR)/icon-1024.png
	cp $< $@

$(ICON_DIR)/icon-%.png: assets/icon-source.svg
	@mkdir -p $(ICON_DIR)
	rsvg-convert -w $* -h $* -o $@ $<

clean:
	rm -rf build

test:
	go test ./...

# --- Windows release packaging (gogio .exe + WiX .msi) ---------------------
# gogio embeds icon (from appicon.png), DPI manifest, version metadata, and
# the GUI subsystem flag. No CGO required for Windows targets in Gio v0.9.

dist-windows: msi-windows-amd64 msi-windows-arm64

dist-windows-amd64: appicon.png
	@mkdir -p $(DIST_DIR)
	@rm -f *.syso
	$(GOGIO) -target windows -arch amd64 -appid $(APP_ID) -version $(SEMVER) -ldflags "$(LDFLAGS_RELEASE)" -o $(DIST_DIR)/Locorum-windows-amd64.exe .
	@rm -f *.syso

dist-windows-arm64: appicon.png
	@mkdir -p $(DIST_DIR)
	@rm -f *.syso
	$(GOGIO) -target windows -arch arm64 -appid $(APP_ID) -version $(SEMVER) -ldflags "$(LDFLAGS_RELEASE)" -o $(DIST_DIR)/Locorum-windows-arm64.exe .
	@rm -f *.syso

msi-windows-amd64: dist-windows-amd64
	$(WIX) build -arch x64 \
		-d Version=$(SEMVER) \
		-d SourceExe=$(DIST_DIR)/Locorum-windows-amd64.exe \
		-o $(DIST_DIR)/Locorum-$(VERSION)-windows-amd64.msi \
		packaging/windows/locorum.wxs

msi-windows-arm64: dist-windows-arm64
	$(WIX) build -arch arm64 \
		-d Version=$(SEMVER) \
		-d SourceExe=$(DIST_DIR)/Locorum-windows-arm64.exe \
		-o $(DIST_DIR)/Locorum-$(VERSION)-windows-arm64.msi \
		packaging/windows/locorum.wxs

# --- Linux release packaging (stripped binary + tar.gz) --------------------
# 0.x ships only the binary plus LICENSE/README/.desktop/icon, bundled in a
# tarball. Users on Ubuntu/Debian/Arch extract and run the binary directly,
# or copy locorum.desktop into ~/.local/share/applications/ for a menu entry.
# Native packages (.deb, AppImage, AUR PKGBUILD) deferred to a later phase.
#
# arm64: cross-compiling from amd64 needs the aarch64 GCC toolchain plus arm64
# X11/Wayland headers (Gio's Linux backend is CGO). Use a native arm64 runner
# in CI; locally, the dist-linux-arm64 target will fail without that toolchain.

dist-linux: tarball-linux-amd64 tarball-linux-arm64

dist-linux-amd64:
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS_RELEASE)" -o $(DIST_DIR)/locorum-linux-amd64 .

dist-linux-arm64:
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS_RELEASE)" -o $(DIST_DIR)/locorum-linux-arm64 .

tarball-linux-amd64: dist-linux-amd64 icons
	@mkdir -p $(DIST_DIR)/stage-linux-amd64/locorum-$(VERSION)-linux-amd64
	cp $(DIST_DIR)/locorum-linux-amd64 $(DIST_DIR)/stage-linux-amd64/locorum-$(VERSION)-linux-amd64/locorum
	cp LICENSE README.md packaging/linux/locorum.desktop $(DIST_DIR)/stage-linux-amd64/locorum-$(VERSION)-linux-amd64/
	cp $(ICON_DIR)/icon-256.png $(DIST_DIR)/stage-linux-amd64/locorum-$(VERSION)-linux-amd64/locorum.png
	tar -czf $(DIST_DIR)/locorum-$(VERSION)-linux-amd64.tar.gz -C $(DIST_DIR)/stage-linux-amd64 locorum-$(VERSION)-linux-amd64
	rm -rf $(DIST_DIR)/stage-linux-amd64

tarball-linux-arm64: dist-linux-arm64 icons
	@mkdir -p $(DIST_DIR)/stage-linux-arm64/locorum-$(VERSION)-linux-arm64
	cp $(DIST_DIR)/locorum-linux-arm64 $(DIST_DIR)/stage-linux-arm64/locorum-$(VERSION)-linux-arm64/locorum
	cp LICENSE README.md packaging/linux/locorum.desktop $(DIST_DIR)/stage-linux-arm64/locorum-$(VERSION)-linux-arm64/
	cp $(ICON_DIR)/icon-256.png $(DIST_DIR)/stage-linux-arm64/locorum-$(VERSION)-linux-arm64/locorum.png
	tar -czf $(DIST_DIR)/locorum-$(VERSION)-linux-arm64.tar.gz -C $(DIST_DIR)/stage-linux-arm64 locorum-$(VERSION)-linux-arm64
	rm -rf $(DIST_DIR)/stage-linux-arm64

# --- macOS release packaging (gogio universal .app + create-dmg) -----------
# gogio embeds icon (.icns from appicon.png), Info.plist, version metadata,
# and produces a universal binary (amd64 + arm64) inside one .app.
# Both gogio's macOS target AND create-dmg use Apple-only tools (iconutil,
# lipo, hdiutil) — these targets MUST run on a macOS host.
# Unsigned per D1: first-launch users hit Gatekeeper and must right-click → Open.

dist-macos: dmg-macos

dist-macos-app: appicon.png
	@mkdir -p $(DIST_DIR)
	$(GOGIO) -target macos -arch amd64,arm64 -appid $(APP_ID) -version $(SEMVER) -ldflags "$(LDFLAGS_RELEASE)" -o $(DIST_DIR)/Locorum.app .

dmg-macos: dist-macos-app
	bash packaging/macos/make-dmg.sh $(DIST_DIR)/Locorum.app $(DIST_DIR)/Locorum-$(VERSION)-macos-universal.dmg "$(VERSION)"
