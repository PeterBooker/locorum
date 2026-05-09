# Changelog

All notable user-visible changes to Locorum are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

`scripts/release.sh` refuses to tag a release if the `Unreleased` section is
empty.

## [Unreleased]

### Added

- Testing & release engineering plan (`TESTING.md`) covering 13 phases:
  foundations, test architecture, cross-platform CI matrix, real-Docker
  integration tests, per-package coverage gates, fuzz targets, race + leak
  detection, migration up/back tests, soak/chaos tests, performance
  regression detection, vulnerability + supply-chain scanning, release
  engineering (cosign keyless + SLSA L3 + reproducible builds), GUI
  property tests.
- Linter configuration (`.golangci.yml`) — 17 linters, security-tilted.
- Renovate config (`renovate.json`) auto-pins GitHub Action digests and
  groups Go module bumps.
- `internal/testutil/` — shared test fixtures, golden-file helpers,
  goroutine + Docker leak assertions.
- `internal/storage/fake` and `internal/tls/fake` packages.
- Real-Docker integration test suite (`-tags=integration`).
- Coverage gate via `go-test-coverage` with per-package floors.
- Fuzz targets for slug normalisation, configyaml roundtrip, hook env
  interpolation, import-DB regex preprocessors, path normalisation, site
  validation, genmark detection, hook YAML parsing.
- Forward + back migration tests, schema-drift snapshot.
- Soak + chaos test suite (nightly).
- Performance benchmarks + benchstat compare gate.
- govulncheck, osv-scanner, Trivy in CI.
- Release pipeline: SBOM (Syft), cosign keyless signatures, SLSA Level 3
  provenance, reproducibility check.
- `docs/VERIFY.md` walks users through verifying a release artifact.
- `docs/RELEASE_SMOKE.md` — required pre-tag manual smoke checklist.

### Changed

### Deprecated

### Removed

### Fixed

### Security

- Pinned Docker image digests in `internal/version/images.go` defend
  against tag-mutation attacks.
- All GitHub Actions pinned via Renovate digest mode.
- Vulnerability scanning gates every PR (`govulncheck`).
