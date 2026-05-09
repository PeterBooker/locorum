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

GOGIO   ?= gogio
# WIX_BIN, not WIX: the windows-latest runner preinstalls WiX 3.14 and sets
# the WIX env var to that install dir, which would override `?= wix`.
WIX_BIN ?= wix

# Bundled mkcert. Pinned to match internal/tls/install.go's MkcertVersion;
# downloaded from the upstream GitHub release into build/vendor/mkcert and
# copied next to the locorum binary in each release artifact. Locorum's
# binary resolver prefers a sibling mkcert before falling back to $PATH or
# auto-download, so a bundled binary turns this into a true zero-dependency
# install.
MKCERT_VERSION := v1.4.4
MKCERT_DIR     := build/vendor/mkcert
MKCERT_BASE    := https://github.com/FiloSottile/mkcert/releases/download/$(MKCERT_VERSION)

.PHONY: build linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64 windows-arm64 all clean test icons \
        dist-windows dist-windows-amd64 dist-windows-arm64 \
        msi-windows-amd64 msi-windows-arm64 \
        dist-linux dist-linux-amd64 dist-linux-arm64 \
        tarball-linux-amd64 tarball-linux-arm64 \
        dist-macos dist-macos-app dmg-macos \
        mkcert-binaries \
        lint test-race test-cover test-cover-check test-cover-baseline \
        fuzz fuzz-nightly integration integration-prepull \
        vuln sbom ci release-preflight reproducibility-check schema-snapshot \
        bench bench-compare

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
	rm -rf build coverage.out coverage.html

test:
	go test ./...

# --- Quality gates ---------------------------------------------------------
# `ci` is the gate every PR must pass. `release-preflight` adds integration
# + reproducibility for tagging. See TESTING.md for the full plan.

GOLANGCI_VERSION  ?= v2.6.0
GOTESTCOV_VERSION ?= v2.16.0
GOVULNCHECK_VER   ?= latest
SYFT_VERSION      ?= v1.46.0
GOLEAK_VERSION    ?= v1.3.0

GOBIN := $(shell go env GOPATH)/bin

lint:
	@command -v golangci-lint >/dev/null 2>&1 || \
		(echo "installing golangci-lint $(GOLANGCI_VERSION)"; \
		 go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION))
	$(GOBIN)/golangci-lint run ./...

test-race:
	go test -count=1 -race ./...

# Coverage profile. Used by test-cover-check (gating) and CI artifact upload.
test-cover:
	go test -count=1 -coverprofile=coverage.out -covermode=atomic ./...
	@go tool cover -func=coverage.out | tail -n 1

# Per-package floors enforced via .testcoverage.yml (D3, D16).
test-cover-check: test-cover
	@command -v go-test-coverage >/dev/null 2>&1 || \
		(echo "installing go-test-coverage $(GOTESTCOV_VERSION)"; \
		 go install github.com/vladopajic/go-test-coverage/v2@$(GOTESTCOV_VERSION))
	$(GOBIN)/go-test-coverage --config=./.testcoverage.yml

# One-shot: print per-package coverage so floors can be set at observed - 2pp.
# Run once when authoring or rebaselining .testcoverage.yml.
test-cover-baseline: test-cover
	@./scripts/coverage-baseline.sh coverage.out

# Short fuzz seeds in CI (10s/target). Long-running fuzz lives in fuzz-nightly.
FUZZ_TIME ?= 10s
fuzz:
	@./scripts/fuzz.sh $(FUZZ_TIME)

fuzz-nightly: FUZZ_TIME=10m
fuzz-nightly: fuzz

# Real-Docker integration tests. Build tag gates compile-time inclusion;
# the suite is opt-in via the `run-integration` PR label or push to main.
integration:
	@command -v docker >/dev/null 2>&1 || (echo "ERROR: docker not on PATH" && exit 1)
	@docker info >/dev/null 2>&1 || (echo "ERROR: docker daemon not reachable" && exit 1)
	go test -count=1 -tags=integration -timeout=30m \
		./internal/sites/... \
		./internal/router/... \
		./internal/docker/...

# Pre-pull every image referenced in internal/version/images.go so the
# integration tests don't fight Docker Hub rate limits.
integration-prepull:
	@./scripts/integration-prepull.sh

# govulncheck: Go vulnerability database. Required gate.
vuln:
	@command -v govulncheck >/dev/null 2>&1 || \
		(echo "installing govulncheck"; \
		 go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VER))
	$(GOBIN)/govulncheck ./...

# CycloneDX + SPDX SBOM via syft. Shipped alongside every release artifact.
sbom:
	@command -v syft >/dev/null 2>&1 || \
		(echo "installing syft $(SYFT_VERSION)"; \
		 curl -sSfL https://raw.githubusercontent.com/anchore/syft/main/install.sh | sh -s -- -b $(GOBIN) $(SYFT_VERSION))
	@mkdir -p $(DIST_DIR)
	$(GOBIN)/syft . -o cyclonedx-json=$(DIST_DIR)/sbom-cyclonedx.json -o spdx-json=$(DIST_DIR)/sbom-spdx.json

# Schema-drift detection. Refuses to commit if the live schema differs from
# the checked-in golden without an accompanying migration.
schema-snapshot:
	@./scripts/schema-snapshot.sh

# Performance benchmarks. `bench-compare` runs main + HEAD and benchstat-diffs.
bench:
	go test -bench=. -benchmem -count=10 -run=^$$ -benchtime=1s ./...

bench-compare:
	@./scripts/bench-compare.sh

# All non-integration gates that must be green before merge.
ci: lint vuln test-race test-cover-check fuzz

# Pre-tag gate: everything `ci` does + integration + reproducibility + sbom.
release-preflight: ci integration reproducibility-check sbom

reproducibility-check:
	@./scripts/reproducibility-check.sh

# --- Bundled mkcert ---------------------------------------------------------
# Each rule downloads one platform's mkcert into build/vendor/mkcert/<os>-<arch>/
# and chmods it executable. Release-packaging targets depend on the matching
# rule and copy the binary alongside the locorum executable so users never
# need to install mkcert themselves.

mkcert-binaries: \
        $(MKCERT_DIR)/linux-amd64/mkcert \
        $(MKCERT_DIR)/linux-arm64/mkcert \
        $(MKCERT_DIR)/darwin-amd64/mkcert \
        $(MKCERT_DIR)/darwin-arm64/mkcert \
        $(MKCERT_DIR)/windows-amd64/mkcert.exe \
        $(MKCERT_DIR)/windows-arm64/mkcert.exe

$(MKCERT_DIR)/linux-amd64/mkcert:
	@mkdir -p $(dir $@)
	curl -fsSL $(MKCERT_BASE)/mkcert-$(MKCERT_VERSION)-linux-amd64 -o $@
	chmod +x $@

$(MKCERT_DIR)/linux-arm64/mkcert:
	@mkdir -p $(dir $@)
	curl -fsSL $(MKCERT_BASE)/mkcert-$(MKCERT_VERSION)-linux-arm64 -o $@
	chmod +x $@

$(MKCERT_DIR)/darwin-amd64/mkcert:
	@mkdir -p $(dir $@)
	curl -fsSL $(MKCERT_BASE)/mkcert-$(MKCERT_VERSION)-darwin-amd64 -o $@
	chmod +x $@

$(MKCERT_DIR)/darwin-arm64/mkcert:
	@mkdir -p $(dir $@)
	curl -fsSL $(MKCERT_BASE)/mkcert-$(MKCERT_VERSION)-darwin-arm64 -o $@
	chmod +x $@

$(MKCERT_DIR)/windows-amd64/mkcert.exe:
	@mkdir -p $(dir $@)
	curl -fsSL $(MKCERT_BASE)/mkcert-$(MKCERT_VERSION)-windows-amd64.exe -o $@

$(MKCERT_DIR)/windows-arm64/mkcert.exe:
	@mkdir -p $(dir $@)
	curl -fsSL $(MKCERT_BASE)/mkcert-$(MKCERT_VERSION)-windows-arm64.exe -o $@

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

msi-windows-amd64: dist-windows-amd64 $(MKCERT_DIR)/windows-amd64/mkcert.exe
	$(WIX_BIN) build -arch x64 \
		-ext WixToolset.UI.wixext \
		-d Version=$(SEMVER) \
		-d SourceExe=$(DIST_DIR)/Locorum-windows-amd64.exe \
		-d SourceMkcert=$(MKCERT_DIR)/windows-amd64/mkcert.exe \
		-o $(DIST_DIR)/Locorum-$(VERSION)-windows-amd64.msi \
		packaging/windows/locorum.wxs

msi-windows-arm64: dist-windows-arm64 $(MKCERT_DIR)/windows-arm64/mkcert.exe
	$(WIX_BIN) build -arch arm64 \
		-ext WixToolset.UI.wixext \
		-d Version=$(SEMVER) \
		-d SourceExe=$(DIST_DIR)/Locorum-windows-arm64.exe \
		-d SourceMkcert=$(MKCERT_DIR)/windows-arm64/mkcert.exe \
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

tarball-linux-amd64: dist-linux-amd64 icons $(MKCERT_DIR)/linux-amd64/mkcert
	@mkdir -p $(DIST_DIR)/stage-linux-amd64/locorum-$(VERSION)-linux-amd64
	cp $(DIST_DIR)/locorum-linux-amd64 $(DIST_DIR)/stage-linux-amd64/locorum-$(VERSION)-linux-amd64/locorum
	cp $(MKCERT_DIR)/linux-amd64/mkcert $(DIST_DIR)/stage-linux-amd64/locorum-$(VERSION)-linux-amd64/mkcert
	cp LICENSE README.md packaging/linux/locorum.desktop $(DIST_DIR)/stage-linux-amd64/locorum-$(VERSION)-linux-amd64/
	cp $(ICON_DIR)/icon-256.png $(DIST_DIR)/stage-linux-amd64/locorum-$(VERSION)-linux-amd64/locorum.png
	tar -czf $(DIST_DIR)/locorum-$(VERSION)-linux-amd64.tar.gz -C $(DIST_DIR)/stage-linux-amd64 locorum-$(VERSION)-linux-amd64
	rm -rf $(DIST_DIR)/stage-linux-amd64

tarball-linux-arm64: dist-linux-arm64 icons $(MKCERT_DIR)/linux-arm64/mkcert
	@mkdir -p $(DIST_DIR)/stage-linux-arm64/locorum-$(VERSION)-linux-arm64
	cp $(DIST_DIR)/locorum-linux-arm64 $(DIST_DIR)/stage-linux-arm64/locorum-$(VERSION)-linux-arm64/locorum
	cp $(MKCERT_DIR)/linux-arm64/mkcert $(DIST_DIR)/stage-linux-arm64/locorum-$(VERSION)-linux-arm64/mkcert
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

dist-macos-app: appicon.png $(MKCERT_DIR)/darwin-amd64/mkcert $(MKCERT_DIR)/darwin-arm64/mkcert
	@mkdir -p $(DIST_DIR)
	rm -rf $(DIST_DIR)/Locorum.app $(DIST_DIR)/macos-staging
	mkdir -p $(DIST_DIR)/macos-staging
	# gogio v0.9.0 with -arch amd64,arm64 nests per-arch .apps inside the
	# output path (Locorum.app/Locorum_{arch}.app) rather than producing
	# a single universal bundle. Build into a staging dir, take the arm64
	# .app as the base, and lipo-merge in the amd64 binary so the final
	# Locorum.app holds one universal binary.
	$(GOGIO) -target macos -arch amd64,arm64 -appid $(APP_ID) -version $(SEMVER) -ldflags "$(LDFLAGS_RELEASE)" -o $(DIST_DIR)/macos-staging/Locorum.app .
	cp -R $(DIST_DIR)/macos-staging/Locorum.app/Locorum_arm64.app $(DIST_DIR)/Locorum.app
	lipo -create -output $(DIST_DIR)/Locorum.app/Contents/MacOS/Locorum \
		$(DIST_DIR)/macos-staging/Locorum.app/Locorum_amd64.app/Contents/MacOS/Locorum \
		$(DIST_DIR)/macos-staging/Locorum.app/Locorum_arm64.app/Contents/MacOS/Locorum
	chmod +x $(DIST_DIR)/Locorum.app/Contents/MacOS/Locorum
	rm -rf $(DIST_DIR)/macos-staging
	# Drop a universal mkcert next to the locorum binary inside the .app
	# bundle so resolveBinary finds it without touching $$PATH. lipo merges
	# the two architecture-specific binaries into one fat binary; macOS
	# picks the right slice at exec time.
	lipo -create -output $(DIST_DIR)/Locorum.app/Contents/MacOS/mkcert \
		$(MKCERT_DIR)/darwin-amd64/mkcert \
		$(MKCERT_DIR)/darwin-arm64/mkcert
	chmod +x $(DIST_DIR)/Locorum.app/Contents/MacOS/mkcert

dmg-macos: dist-macos-app
	bash packaging/macos/make-dmg.sh $(DIST_DIR)/Locorum.app $(DIST_DIR)/Locorum-$(VERSION)-macos-universal.dmg "$(VERSION)"
