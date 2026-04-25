# Locorum Desktop Packaging Plan

Goal: produce installable, distributable Locorum builds for **Linux, Windows, and macOS**, replacing the current "raw cross-compiled binary" Makefile output with proper desktop apps (icons, metadata, install flow).

Code signing is deferred (see § 6); first releases will be unsigned with documented user workarounds.

---

## 1. Current state

| Concern | Status |
|---|---|
| Cross-compile to all 4 GOOS/GOARCH | ✅ `make all` produces raw binaries |
| App icon embedded in binary | ❌ Only `assets/logo.svg` exists; nothing embedded |
| App metadata (version, publisher, bundle ID) | ❌ Nothing baked into binaries |
| Windows manifest (DPI awareness, no console) | ❌ Console window appears; no manifest |
| macOS `.app` bundle / `Info.plist` / `.icns` | ❌ Bare binary only |
| Linux desktop entry / icon install path | ❌ None |
| Installer (MSI / DMG / .deb / AppImage) | ❌ None |
| CI / release automation | ❌ No `.github/` directory |
| Version stamped into binary | ❌ No `Version` var or build flag |
| LICENSE | ✅ MIT, 2026 Peter Booker |

The existing `Makefile` only does `go build` per OS/arch — fine for development, not shippable.

---

## 2. Decisions (settled)

| # | Decision | Choice |
|---|---|---|
| D1 | macOS Apple Developer Program | **Defer** — ship `.app` + `.dmg` unsigned; README documents Gatekeeper "right-click → Open" workaround. |
| D2 | Windows code signing | **Defer** — ship unsigned MSI; README documents SmartScreen "More info → Run anyway" workaround. |
| D3 | Linux distribution formats | **`.tar.gz` only for 0.x** (revised 2026-04-25). Users on Ubuntu/Debian/Arch extract and run the binary directly; `.desktop` file shipped inside the tarball for optional manual menu integration. AppImage + `.deb` + AUR PKGBUILD deferred to a later phase. Skip Flatpak/Snap permanently (sandboxing fights Docker socket). |
| D4 | Bundle / app ID | `com.peterbooker.locorum` (placeholder — confirm before first publish; immutable thereafter). |
| D5 | CI provider | **GitHub Actions.** |
| D6 | Release tooling | **GoReleaser.** |
| D7 | Icon source | Generate raster PNGs from `assets/logo.svg` (redrawn with ~10% margin for macOS squircle template). |
| D8 | Distribution channels beyond GitHub Releases | **Defer.** Homebrew tap, Scoop bucket, AUR added post-1.0. |
| D9 | Starting version | `0.1.0`. |
| D10 | Architectures | **amd64 + arm64** for Linux, Windows, macOS. macOS shipped as one universal binary. |
| D11 | Installer formats | Linux: `.tar.gz` only for 0.x (see D3). Windows: **unsigned MSI** (WiX v4). macOS: `.app` inside `.dmg`. |
| D12 | License | **MIT** — `LICENSE` file added in this commit. |
| D13 | In-app auto-update | Out of scope. Revisit post-1.0. |

---

## 3. Phased plan

### Phase 1 — Foundations

1. **Add a version package.** `internal/version/version.go` exporting `Version`, `Commit`, `Date` strings (defaults `dev`, `none`, `unknown`). Override at build time via `-ldflags "-X github.com/PeterBooker/locorum/internal/version.Version=$(git describe --tags --always)"`.
2. **Update `Makefile`** to inject version ldflags into every `go build` and add `-s -w` for release builds. `make build` (dev) stays un-stripped for stack traces.
3. **Generate raster app icons** from `assets/logo.svg`:
   - Add `assets/icon-source.svg` — redrawn logo with ~10% safe margin (the existing logo is a tight 48×48 with no margin).
   - Add `make icons` target using `rsvg-convert` to produce `assets/icons/icon-{16,32,48,64,128,256,512,1024}.png`. Commit the PNGs.
   - Add `appicon.png` (1024×1024) at the repo root — gogio convention; without it, gogio falls back to a generic Go icon for Windows/macOS targets.
4. ~~**Wire window icon into Gio.**~~ **Skipped** — Gio v0.9 has no `app.Icon` Option. Window icons are picked up per-platform at packaging time: gogio embeds a Windows `.syso` resource read by `LoadImage(hInst, iconID, ...)`; macOS reads the `.icns` from the bundle; Linux reads the `Icon=` line from the installed `.desktop` file. Nothing to do here in Phase 1.
5. **Update `README.md`** prerequisites: Go 1.25 (not 1.23). Clarify CGO is needed on Linux + macOS but **not** on Windows for Gio v0.9 — drop the mingw-gcc instructions.
6. **`LICENSE`** — done (MIT).

### Phase 2 — Per-platform packaging

#### 2a. Windows (`.exe` with icon + manifest, unsigned MSI)

- **Binary build**: `gogio -target windows -arch amd64,arm64 -appid com.peterbooker.locorum -version 0.1.0.1 -icon appicon.png -ldflags "-s -w -X .../version.Version=0.1.0" -o build/dist/Locorum.exe .`
  - Embeds icon, manifest (DPI-aware, no console), VS_VERSIONINFO.
  - No CGO needed → cross-compile from Linux runner.
- **Installer**: WiX v4. Add `packaging/windows/locorum.wxs` declaring product GUID, install dir (`%ProgramFiles%\Locorum`), Start Menu shortcut, and uninstaller. Per-arch builds → `Locorum-<version>-x64.msi` and `Locorum-<version>-arm64.msi`.
- **Why MSI even unsigned**: gives users a real install/uninstall flow via Add-Remove Programs. SmartScreen will warn on first install (documented in README); user clicks "More info → Run anyway".
- **Build host**: WiX v4 runs cross-platform on .NET. Can run on Linux runner via `dotnet tool install -g wix`.

#### 2b. macOS (`.app` universal binary + `.dmg`, unsigned)

- **Binary + bundle**: `gogio -target macos -arch amd64,arm64 -appid com.peterbooker.locorum -version 0.1.0.1 -icon appicon.png -ldflags "-s -w -X .../version.Version=0.1.0" -o build/dist/Locorum.app .`
  - One universal `.app` (Apple's preferred distribution). `lipo` is invoked internally by gogio.
  - Generates `Info.plist`, `.icns` (12 sizes up to 1024×1024 retina).
  - **Requires macOS host** (uses `iconutil`, `lipo`). No cross-compile from Linux.
  - **No `-signkey` / `-notaryid`** flags → unsigned. Gatekeeper will block on first launch; users right-click → Open the first time.
- **`.dmg`**: gogio doesn't make DMGs. `packaging/macos/make-dmg.sh` wraps [`create-dmg`](https://github.com/create-dmg/create-dmg) with a drag-to-Applications layout (500×400 window, `--app-drop-link`). Output: `build/dist/Locorum-<version>-macos-universal.dmg`. Brew install on the macOS runner.
- **README addition (pending Phase 4)**: "Locorum isn't notarized yet — first launch: control-click the app, choose Open, confirm. After that it launches normally."

#### 2c. Linux (stripped binary + `.tar.gz`, amd64 + arm64)

- **Binary**: `go build -ldflags "-s -w -X .../version.Version=0.1.0"` — works for the host arch.
- **arm64 builds**: GitHub Actions has free `ubuntu-24.04-arm` runners for public repos (since 2025) — use them for native Linux arm64. Cross-compiling Linux/arm64 from amd64 needs the aarch64 cross-toolchain plus arm64 X11/Wayland headers; native runner is much simpler.
- **`.desktop` file**: `packaging/linux/locorum.desktop` shipped inside the tarball. Users can copy it to `~/.local/share/applications/` for a menu entry.
- **`.tar.gz`**: bundles `locorum` (binary), `LICENSE`, `README.md`, `locorum.desktop`, `locorum.png` (256×256). Two archives: `locorum-<version>-linux-amd64.tar.gz`, `locorum-<version>-linux-arm64.tar.gz`.
- **Native packages deferred**: AppImage, `.deb`, AUR PKGBUILD all postponed to a later phase. 0.x users on Ubuntu/Debian/Arch run the binary directly. See D3.
- **glibc portability**: builds done on `ubuntu-22.04` (or older LTS) in CI link against an older glibc; a binary built on a rolling-release distro like CachyOS will fail with `GLIBC_X.Y not found` on older Ubuntus. Local `make tarball-linux-amd64` is for testing the build pipeline, not for distribution.
- **No Flatpak/Snap** — see D3.

### Phase 3 — CI release pipeline

- **`.github/workflows/ci.yml`** — on push/PR:
  - Matrix: `ubuntu-latest` (linux-amd64). `go vet`, `go test ./...`, `gofmt -l`, `golangci-lint` (add `.golangci.yml`).
  - Smoke build of Linux binary.
  - Don't run macOS/Windows on every PR (cost) — only on main + tags.
- **`.github/workflows/release.yml`** — on tag matching `[0-9]*` (bare semver, no `v` prefix):
  - **macOS job** (`macos-latest`, arm64 Apple Silicon): `brew install create-dmg librsvg`, install gogio, `make dist-macos VERSION=$tag` produces a universal-binary `.app` and wraps it in a `.dmg`. Uploads artifact.
  - **Windows job** (`ubuntu-22.04`, cross-compile): installs Go + .NET 8 + `wix` global tool + `gogio`, then `make dist-windows VERSION=$tag` produces both amd64 and arm64 MSIs. Uploads artifacts.
  - **Linux amd64 job** (`ubuntu-22.04` for older glibc): `make tarball-linux-amd64`. Uploads.
  - **Linux arm64 job** (`ubuntu-22.04-arm`, native): `make tarball-linux-arm64`. Uploads.
  - **Release job** (`ubuntu-22.04`, depends on all build jobs): downloads all artifacts to `build/dist/`, runs `goreleaser release --clean` (publish-only mode). Goreleaser computes SHA256SUMS, generates changelog from GitHub-native release notes, publishes the GitHub Release with all artifacts.
- **`.goreleaser.yaml`**: publish-only mode. `dist: build/release` keeps goreleaser's state separate from our pre-built artifacts at `build/dist/`. `builds[0].skip: true` and an empty-`ids` archive stub satisfy the schema. `release.extra_files` and `checksum.extra_files` glob in our tarballs and MSIs. When Phase 5 lands (Homebrew tap, Scoop bucket, AUR), switch goreleaser to drive the actual builds and add `brews:` / `scoops:` / `aurs:` blocks.

### Phase 4 — README + first release

1. ✅ Refresh README with end-user install instructions per platform (DMG / MSI / tar.gz from Releases).
2. ✅ Add Gatekeeper + SmartScreen "first run" notes.
3. ⏳ **User action**: tag `0.1.0`, push, watch the release pipeline run. The release lands as a draft — review notes + verify all 5 artifacts uploaded, then click Publish.

---

## 4. Concrete file changes

What lands in the repo when this plan executes:

```
PLAN.md                                  ← this file
LICENSE                                  ← MIT (added)
appicon.png                              ← 1024×1024, gogio convention
internal/version/version.go              ← Version/Commit/Date vars
assets/icon-source.svg                   ← redrawn logo with margin
assets/icons/icon-*.png                  ← generated PNGs (8 sizes)
Makefile                                 ← icons, dist-*, version ldflags
packaging/
  windows/
    locorum.wxs                          ← WiX v4 installer definition
  macos/
    make-dmg.sh                          ← create-dmg wrapper
  linux/
    locorum.desktop                      ← XDG desktop entry (shipped in tarball)
.github/workflows/
  ci.yml                                 ← test + fmt + smoke build on PR
  release.yml                            ← tag → per-platform build → goreleaser publish
.goreleaser.yaml                         ← publish-only release config
README.md                                ← updated install/build sections
main.go                                  ← startup version log
```

`build/` stays gitignored; `build/dist/` holds release artifacts locally.

---

## 5. Out of scope

- **In-app auto-update.** Defer until 1.0 (D13).
- **Mac App Store / Microsoft Store / winget.** Each has separate review processes.
- **Snap / Flatpak.** Sandboxing fights Docker socket access (D3).
- **Homebrew / Scoop / AUR.** Deferred (D8).
- **i18n / translations.** Unrelated to packaging.
- **Telemetry / crash reporting.** Decide separately; affects privacy policy and signing entitlements.

---

## 6. Deferred — re-open when ready

When you decide to enable signing:

- **macOS** ($99/yr Apple Developer): add `Developer ID Application` cert to the macOS runner keychain, pass `-signkey "Developer ID Application: NAME (TEAMID)" -notaryid $APPLE_ID -notarypass $APPLE_APP_PASSWORD -notaryteamid $APPLE_TEAM_ID` to gogio. Drop the README "right-click → Open" note.
- **Windows** ($200–500/yr OV cert, ~$1k EV): post-build, run `signtool sign /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 /a Locorum.exe Locorum.msi`. EV certs skip SmartScreen entirely; OV certs require ~weeks of reputation building. Drop the README "More info → Run anyway" note.
- **Distribution channels** (Homebrew tap, Scoop bucket, AUR): goreleaser has built-in publishers for all three. Set up `peterbooker/homebrew-tap`, `peterbooker/scoop-bucket`, and an `aur` repo, then enable the `brews:` / `scoops:` / `aurs:` blocks in `.goreleaser.yaml`.

---

## 7. Estimated effort

| Phase | Effort |
|---|---|
| Phase 1 (foundations + LICENSE + version + icons) | ½ day |
| Phase 2a Windows (gogio + WiX MSI, amd64+arm64) | 1 day |
| Phase 2b macOS (gogio universal + dmg) | ½ day |
| Phase 2c Linux (tar.gz only for 0.x, amd64+arm64) | ¼ day |
| Phase 3 CI/goreleaser | 1 day |
| Phase 4 README + first release shakedown | ½ day |

**Total: ~4.5 days** of focused work to first automated `0.1.0` release across all six artifacts (Linux amd64/arm64, Windows amd64/arm64, macOS universal).

---

## 8. Ready to start

All decisions resolved. Phase 1 is unblocked — say the word and I'll start with the version package + Makefile + LICENSE wiring, then move through the phases.
