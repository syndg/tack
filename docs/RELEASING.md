# Releasing Tack

Tack ships as a binary-first CLI. Tagged releases publish GitHub release archives, update the Homebrew tap, and keep `go install` and source builds available as fallbacks.

## One-Time Setup

- Create the tap repository: `syndg/homebrew-tack`.
- Create a GitHub token with permission to push to `syndg/homebrew-tack`.
- Add that token to this repo as `HOMEBREW_TAP_GITHUB_TOKEN`.
- Ensure GitHub Actions can write release contents for this repository.

## Release Channels

### GitHub Release Binaries

Pushing a tag that matches `v*` runs `.github/workflows/release.yml` and GoReleaser.

Published archives:

- `tack_Darwin_arm64.tar.gz`
- `tack_Darwin_x86_64.tar.gz`
- `tack_Linux_arm64.tar.gz`
- `tack_Linux_x86_64.tar.gz`
- `checksums.txt`

### Homebrew Tap

GoReleaser updates `syndg/homebrew-tack` with a cask for:

```bash
brew tap syndg/tack
brew install --cask tack
```

This is intentionally a tap first. Official Homebrew core can come later after Tack has stable tagged releases and broader usage.

### Install Script

Users can install the latest release with:

```bash
curl -fsSL https://raw.githubusercontent.com/syndg/tack/main/install.sh | sh
```

Install a specific version:

```bash
curl -fsSL https://raw.githubusercontent.com/syndg/tack/main/install.sh | TACK_VERSION=v0.1.0-alpha.1 sh
```

### Go Install

Go users can install from a tag:

```bash
go install github.com/syndg/tack/cmd/tack@v0.1.0-alpha.1
```

### Source Build

Source build remains the escape hatch:

```bash
git clone https://github.com/syndg/tack.git
cd tack
go build -o tack ./cmd/tack
```

## Cut A Release

1. Ensure `go test ./...` passes.
2. Update release notes or `CHANGELOG.md` if present.
3. Tag and push:

```bash
git tag v0.1.0-alpha.1
git push origin v0.1.0-alpha.1
```

4. Confirm the GitHub release contains all archives and `checksums.txt`.
5. Confirm the Homebrew tap cask was updated.
6. Test install from GitHub release, Homebrew, and `go install`.
