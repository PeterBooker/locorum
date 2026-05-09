# Building Locorum

This document describes the supported build environment and the
reproducibility contract.

## Quick start

```bash
make build      # ./build/bin/locorum (dev build, default ldflags)
make all        # cross-compile for every platform Locorum ships
make test       # full unit test suite
make ci         # the gate every PR must pass (lint+vuln+race+cover+fuzz)
```

## Toolchain

| Tool | Version | Source |
|---|---|---|
| Go | 1.25 | go.mod, `actions/setup-go` |
| gcc | platform default | system package |
| Gio Linux deps | listed in `scripts/install-gio-deps-linux.sh` | apt-get |
| WiX (Windows MSI) | 5.0.2 | `dotnet tool install` |
| gogio | v0.9.0 | `go install gioui.org/cmd/gogio@v0.9.0` |
| create-dmg (macOS) | latest | `brew install create-dmg` |

Every CI job pins these via Renovate digest mode (`renovate.json`).

## Reproducibility

`scripts/reproducibility-check.sh` builds the binary twice from the same
commit and compares byte-for-byte. Two consecutive runs must produce
identical output. The script is a release-preflight gate.

The supported reproducibility environment is:

| Aspect | Value |
|---|---|
| OS | Ubuntu 22.04 (`ubuntu-22.04` GitHub Actions runner) |
| Architecture | linux/amd64 |
| Go | exactly 1.25 — bumps go through a dedicated reproducibility-validating PR |
| CGO | enabled (Gio's display backend); pinned host gcc |
| Build flags | `-trimpath -buildvcs=false`, `SOURCE_DATE_EPOCH` set to the commit time |
| ldflags | `-s -w -X version.Version=...` (deterministic) |

We do **not** promise reproducibility on macOS or Windows. The Apple
toolchain encodes per-build absolute paths in linker output that
`-trimpath` cannot fully erase; the same is true of the Microsoft
linker on Windows. Linux is the reproducibility-guarantee surface.

## Build artifacts

Each release ships, per platform:

- **Linux** (amd64 + arm64): `locorum-<ver>-linux-<arch>.tar.gz`
  containing the `locorum` binary, `mkcert` sibling, `LICENSE`,
  `README.md`, `locorum.desktop`, and `locorum.png`.
- **Windows** (amd64 + arm64): `Locorum-<ver>-windows-<arch>.msi`,
  built via gogio + WiX, embedding mkcert as a sibling.
- **macOS** (universal): `Locorum-<ver>-macos-universal.dmg`, built via
  gogio + create-dmg, with a fat (amd64+arm64) binary and bundled
  universal mkcert.

Plus, for every artifact:
- `<name>.sig` + `<name>.cert` (cosign keyless)
- `<name>.cdx.json` + `<name>.spdx.json` (SBOM)
- `SHA256SUMS` (across the entire release)
- `locorum.intoto.jsonl` (SLSA L3 provenance, single file per release)

## Running the build pipeline locally

A complete pre-tag run mimics CI's release path:

```bash
make ci                              # required gates
make integration                     # real-Docker tests
make sbom                            # generate SBOM
./scripts/reproducibility-check.sh   # bit-for-bit reproducibility
```

If everything is green, `./scripts/release.sh X.Y.Z` tags + pushes.
