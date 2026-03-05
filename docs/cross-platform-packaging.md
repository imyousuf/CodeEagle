# Cross-Platform Packaging & Distribution

## Overview

CodeEagle currently ships as CLI-only tarballs for Linux (amd64/arm64) and macOS (amd64/arm64) via GitHub Actions. This spec adds:

1. **Windows builds** (amd64/arm64)
2. **All build tags enabled** (`faces`, `app`, `desktop`, `production`) — one binary per platform with full features
3. **Native installation packages** per OS with dependency management
4. **Package manager distribution** — Homebrew (macOS/Linux), WinGet (Windows), APT/DNF (Linux)

## Current State

- **CI** (`ci.yml`): Runs tests + lint on `ubuntu-latest` only, no optional tags
- **Release** (`release.yml`): Triggered by `v*` tags, builds 4 tarballs (linux-amd64, linux-arm64, darwin-amd64, darwin-arm64) without optional build tags
- **Dev Release** (`release-dev.yml`): Triggered by main push, same 4 tarballs as dev pre-release
- **No Windows builds** in any workflow
- **No packaging** — only raw tarballs with manual extraction
- **No optional features** — builds exclude `faces` (OpenCV), `app` (Wails desktop UI)

## Design Principles

### Feature Graceful Degradation

All features are compiled into a single binary, but capabilities degrade gracefully based on runtime configuration and available services. The binary is always the same — what varies is the user's environment:

| Feature | Requires | Without It |
|---------|----------|------------|
| RAG / semantic search | Embedding provider (Ollama or Vertex AI) | Falls back to keyword-only search |
| AI agents (plan, design, review, ask) | LLM provider configured | Error: "no LLM provider configured" |
| Doc topic extraction | Docs LLM provider (Ollama or Vertex AI) | Docs indexed without topics |
| Image description | Multimodal LLM provider | Images indexed as metadata-only (filename, EXIF) |
| Face detection | OpenCV libs at runtime | `faces` features silently disabled |
| Desktop UI (`codeeagle app`) | WebView runtime (webkit2gtk / WebView2) | Error: "WebView not available" |

This means packaging declares native dependencies as **recommended** (not required) where possible, so users can install the CLI without all optional deps and still use core features.

### Nightly Dev Releases Continue

The existing `release-dev.yml` nightly build (triggered on every push to `main`) continues unchanged in purpose. It produces a `dev` pre-release on GitHub Releases. The only changes are:
- Add Windows builds to the matrix
- Enable all build tags
- Produce packages (`.deb`, `.rpm`, `.zip`, NSIS installer) alongside tarballs
- **No** WinGet or Homebrew updates for dev builds (registries only track stable releases)

### Auto-Update Continues

The existing `codeeagle update` self-update mechanism (`internal/cli/update.go`) continues working. Changes needed:
- `buildDownloadURL()` must handle Windows: `.zip` instead of `.tar.gz`, `codeeagle.exe` instead of `codeeagle`
- `downloadAndInstall()` must handle `.zip` extraction on Windows (currently only handles `.tar.gz`)
- On Windows with NSIS-installed binary: update replaces the binary in-place at `$PROGRAMFILES\CodeEagle\codeeagle.exe` (same rename-and-replace strategy)
- Dev builds auto-check every 6 hours on startup (existing behavior, no change)

## Target State

### Build Matrix

| Platform | Arch | Runner | CGO | Native Deps |
|----------|------|--------|-----|-------------|
| Linux | amd64 | `ubuntu-22.04` | Yes | libopencv-dev, libwebkit2gtk-4.1-dev, nodejs |
| Linux | arm64 | `ubuntu-22.04` (cross) | Yes | Cross opencv + webkit2gtk (see Appendix A) |
| macOS | amd64 | `macos-13` | Yes | opencv (brew), node |
| macOS | arm64 | `macos-14` (Apple Silicon) | Yes | opencv (brew), node |
| Windows | amd64 | `windows-latest` | Yes | opencv (pre-built), node, WebView2 |
| Windows | arm64 | `windows-latest` (cross) | Yes | Cross opencv (see Appendix B) |

All builds use tags: `faces app desktop production`.

### Package Formats

| Platform | Package Format | Tool | Contains |
|----------|---------------|------|----------|
| Linux amd64 | `.deb` | nfpm | Binary + man page |
| Linux amd64 | `.rpm` | nfpm | Binary + man page |
| Linux arm64 | `.deb`, `.rpm` | nfpm | Binary + man page |
| macOS (universal) | Homebrew formula | GoReleaser / manual | Binary via tap |
| macOS (universal) | Homebrew cask | GoReleaser / manual | `.app` bundle in `.dmg` |
| Windows amd64 | NSIS installer `.exe` | NSIS / Wails | Binary + bundled DLLs |
| Windows arm64 | NSIS installer `.exe` | NSIS / Wails | Binary + bundled DLLs |

### Distribution Channels

| Channel | Registry | Manifest Location | Install Command |
|---------|----------|-------------------|-----------------|
| **Homebrew** | `imyousuf/CodeEagle` repo | `Formula/codeeagle.rb` | `brew tap imyousuf/codeeagle https://github.com/imyousuf/CodeEagle && brew install codeeagle` |
| **WinGet** | `microsoft/winget-pkgs` | Submitted via `wingetcreate` | `winget install imyousuf.CodeEagle` |
| **APT** (PPA) | Launchpad or GitHub Releases | `.deb` packages | `sudo apt install ./codeeagle_*.deb` or PPA |
| **DNF** (COPR) | Fedora COPR or GitHub Releases | `.rpm` packages | `sudo dnf install ./codeeagle_*.rpm` or COPR |
| **GitHub Releases** | `imyousuf/CodeEagle` | Tarballs + installers | Manual download |

## Implementation

### Phase 1: Build Infrastructure

#### 1.1 Add nfpm Configuration

Create `nfpm.yaml` at project root for Linux packages:

```yaml
name: codeeagle
arch: "${GOARCH}"
platform: linux
version: "${VERSION}"
maintainer: "imyousuf <imyousuf@github.com>"
description: "AI-powered code intelligence with knowledge graph"
homepage: "https://github.com/imyousuf/CodeEagle"
license: "Apache-2.0"

contents:
  - src: dist/codeeagle
    dst: /usr/local/bin/codeeagle
    file_info:
      mode: 0755

recommends:
  - libopencv-dev
  - libwebkit2gtk-4.1-dev

overrides:
  rpm:
    recommends:
      - opencv-devel
      - webkit2gtk4.1-devel
```

#### 1.2 Add NSIS Configuration for Windows

Create `installer.nsi` at project root for Windows installer:

```nsi
!include "MUI2.nsh"

Name "CodeEagle"
OutFile "dist\codeeagle-${ARCH}-setup.exe"
InstallDir "$PROGRAMFILES\CodeEagle"
RequestExecutionLevel admin

!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Section "Install"
  SetOutPath $INSTDIR
  File "dist\codeeagle.exe"
  File "dist\opencv_world*.dll"

  ; Add to PATH
  EnVar::AddValue "PATH" "$INSTDIR"

  ; Create uninstaller
  WriteUninstaller "$INSTDIR\uninstall.exe"

  ; WinGet metadata
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\CodeEagle" \
    "DisplayName" "CodeEagle"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\CodeEagle" \
    "UninstallString" "$INSTDIR\uninstall.exe"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\CodeEagle" \
    "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\CodeEagle" \
    "Publisher" "imyousuf"
SectionEnd

Section "Uninstall"
  Delete "$INSTDIR\codeeagle.exe"
  Delete "$INSTDIR\opencv_world*.dll"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  EnVar::DeleteValue "PATH" "$INSTDIR"
  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\CodeEagle"
SectionEnd
```

#### 1.3 Add Homebrew Formula

Create `Formula/codeeagle.rb`:

```ruby
class Codeeagle < Formula
  desc "AI-powered code intelligence with knowledge graph"
  homepage "https://github.com/imyousuf/CodeEagle"
  version "${VERSION}"
  license "Apache-2.0"

  on_macos do
    on_arm do
      url "https://github.com/imyousuf/CodeEagle/releases/download/v#{version}/codeeagle-darwin-arm64.tar.gz"
      sha256 "${SHA_DARWIN_ARM64}"
    end
    on_intel do
      url "https://github.com/imyousuf/CodeEagle/releases/download/v#{version}/codeeagle-darwin-amd64.tar.gz"
      sha256 "${SHA_DARWIN_AMD64}"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/imyousuf/CodeEagle/releases/download/v#{version}/codeeagle-linux-arm64.tar.gz"
      sha256 "${SHA_LINUX_ARM64}"
    end
    on_intel do
      url "https://github.com/imyousuf/CodeEagle/releases/download/v#{version}/codeeagle-linux-amd64.tar.gz"
      sha256 "${SHA_LINUX_AMD64}"
    end
  end

  depends_on "opencv"

  def install
    bin.install "codeeagle"
  end

  test do
    assert_match "codeeagle version", shell_output("#{bin}/codeeagle version")
  end
end
```

### Phase 2: CI/CD Workflow Changes

#### 2.1 Update `ci.yml` — Add Multi-Platform Testing

```yaml
jobs:
  test:
    strategy:
      matrix:
        os: [ubuntu-22.04, macos-14, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.24"
      - name: Install deps (Linux)
        if: runner.os == 'Linux'
        run: |
          sudo apt-get update
          sudo apt-get install -y libopencv-dev libwebkit2gtk-4.1-dev
      - name: Install deps (macOS)
        if: runner.os == 'macOS'
        run: brew install opencv
      - name: Install deps (Windows)
        if: runner.os == 'Windows'
        run: choco install opencv
      - uses: actions/setup-node@v4
        with:
          node-version: "20"
      - name: Build frontend
        run: cd internal/app/frontend && npm install && npm run build
      - name: Test
        run: go test -race -v ./...
      - name: Build (full)
        run: go build -tags "faces app desktop production" ./cmd/codeeagle
```

#### 2.2 New `release.yml` — Full Packaging Pipeline

Replace the current release workflow with a comprehensive pipeline:

```
Trigger: push tag v*

Jobs:
  1. build-linux-amd64    (ubuntu-22.04)   → binary + .deb + .rpm
  2. build-linux-arm64    (ubuntu-22.04)   → binary + .deb + .rpm (cross-compile)
  3. build-darwin-amd64   (macos-13)       → binary tarball
  4. build-darwin-arm64   (macos-14)       → binary tarball
  5. build-windows-amd64  (windows-latest) → binary + NSIS installer
  6. build-windows-arm64  (windows-latest) → binary + NSIS installer (cross-compile)
  7. create-release       (ubuntu-latest)  → GitHub Release with all artifacts
  8. update-homebrew      (ubuntu-latest)  → Update Formula/codeeagle.rb with new SHAs
  9. update-winget        (ubuntu-latest)  → Submit manifest to microsoft/winget-pkgs
```

Each build job:
1. Installs platform-specific native deps (OpenCV, webkit2gtk/WebView2, Node.js)
2. Builds the React frontend (`npm install && npm run build`)
3. Builds the Go binary with all tags: `-tags "faces app desktop production"`
4. Packages into platform-appropriate format

#### 2.3 Linux Build Job (amd64, detailed)

```yaml
build-linux-amd64:
  runs-on: ubuntu-22.04
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: "1.24"
    - uses: actions/setup-node@v4
      with:
        node-version: "20"

    - name: Install native dependencies
      run: |
        sudo apt-get update
        sudo apt-get install -y \
          libopencv-dev \
          libwebkit2gtk-4.1-dev \
          nfpm

    - name: Build frontend
      run: cd internal/app/frontend && npm install && npm run build

    - name: Build binary
      run: |
        mkdir -p dist
        LDFLAGS="-s -w -X ...Version=$VERSION -X ...Commit=$COMMIT -X ...BuildDate=$DATE"
        go build -tags "faces app desktop production webkit2_41" \
          -ldflags "$LDFLAGS" -o dist/codeeagle ./cmd/codeeagle

    - name: Create tarball
      run: tar -czvf dist/codeeagle-linux-amd64.tar.gz -C dist codeeagle

    - name: Create .deb and .rpm
      run: |
        VERSION=${GITHUB_REF_NAME#v} GOARCH=amd64 nfpm package -p deb -t dist/
        VERSION=${GITHUB_REF_NAME#v} GOARCH=amd64 nfpm package -p rpm -t dist/
```

#### 2.4 macOS Build Job (arm64, detailed)

```yaml
build-darwin-arm64:
  runs-on: macos-14
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: "1.24"
    - uses: actions/setup-node@v4
      with:
        node-version: "20"

    - name: Install OpenCV
      run: brew install opencv

    - name: Build frontend
      run: cd internal/app/frontend && npm install && npm run build

    - name: Build binary
      env:
        GOOS: darwin
        GOARCH: arm64
        CGO_ENABLED: "1"
      run: |
        mkdir -p dist
        go build -tags "faces app desktop production" \
          -ldflags "$LDFLAGS" -o dist/codeeagle ./cmd/codeeagle

    - name: Create tarball
      run: tar -czvf dist/codeeagle-darwin-arm64.tar.gz -C dist codeeagle
```

#### 2.5 Windows Build Job (amd64, detailed)

```yaml
build-windows-amd64:
  runs-on: windows-latest
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version: "1.24"
    - uses: actions/setup-node@v4
      with:
        node-version: "20"

    - name: Install OpenCV
      run: |
        choco install opencv -y
        echo "CGO_CFLAGS=-IC:\tools\opencv\build\include" >> $GITHUB_ENV
        echo "CGO_LDFLAGS=-LC:\tools\opencv\build\x64\vc16\lib -lopencv_world4100" >> $GITHUB_ENV

    - name: Install NSIS
      run: choco install nsis -y

    - name: Build frontend
      run: cd internal/app/frontend && npm install && npm run build

    - name: Build binary
      env:
        CGO_ENABLED: "1"
      run: |
        mkdir dist
        go build -tags "faces app desktop production" `
          -ldflags "$LDFLAGS" -o dist/codeeagle.exe ./cmd/codeeagle

    - name: Bundle DLLs
      run: |
        copy C:\tools\opencv\build\x64\vc16\bin\opencv_world*.dll dist\

    - name: Create installer
      run: |
        makensis /DVERSION=${{ env.VERSION }} /DARCH=amd64 installer.nsi

    - name: Create zip
      run: |
        Compress-Archive -Path dist\codeeagle.exe,dist\opencv_world*.dll `
          -DestinationPath dist\codeeagle-windows-amd64.zip
```

#### 2.6 Homebrew Formula Update Job

```yaml
update-homebrew:
  needs: create-release
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4

    - name: Download checksums
      run: |
        gh release download ${{ github.ref_name }} -p checksums.txt -D dist/
      env:
        GH_TOKEN: ${{ github.token }}

    - name: Update formula
      run: |
        VERSION=${GITHUB_REF_NAME#v}
        SHA_LINUX_AMD64=$(grep linux-amd64 dist/checksums.txt | awk '{print $1}')
        SHA_LINUX_ARM64=$(grep linux-arm64 dist/checksums.txt | awk '{print $1}')
        SHA_DARWIN_AMD64=$(grep darwin-amd64 dist/checksums.txt | awk '{print $1}')
        SHA_DARWIN_ARM64=$(grep darwin-arm64 dist/checksums.txt | awk '{print $1}')

        sed -i "s/version \".*\"/version \"$VERSION\"/" Formula/codeeagle.rb
        # Update each SHA256 in the formula
        # (use a script or template engine for robustness)

    - name: Commit formula update
      run: |
        git config user.name "github-actions[bot]"
        git config user.email "github-actions[bot]@users.noreply.github.com"
        git add Formula/codeeagle.rb
        git commit -m "Update Homebrew formula to ${{ github.ref_name }}"
        git push
```

#### 2.7 WinGet Manifest Submission Job

```yaml
update-winget:
  needs: create-release
  runs-on: windows-latest
  steps:
    - name: Install wingetcreate
      run: |
        Invoke-WebRequest -Uri https://aka.ms/wingetcreate/latest -OutFile wingetcreate.exe

    - name: Submit manifest
      run: |
        $version = "${{ github.ref_name }}".TrimStart("v")
        $amd64Url = "https://github.com/imyousuf/CodeEagle/releases/download/${{ github.ref_name }}/codeeagle-windows-amd64-setup.exe"
        $arm64Url = "https://github.com/imyousuf/CodeEagle/releases/download/${{ github.ref_name }}/codeeagle-windows-arm64-setup.exe"

        .\wingetcreate.exe update imyousuf.CodeEagle `
          --version $version `
          --urls $amd64Url $arm64Url `
          --submit `
          --token ${{ secrets.WINGET_PAT }}
```

### Phase 3: Release Artifacts

Each tagged release will contain:

```
codeeagle-linux-amd64.tar.gz          # Binary tarball
codeeagle-linux-arm64.tar.gz
codeeagle-darwin-amd64.tar.gz
codeeagle-darwin-arm64.tar.gz
codeeagle-windows-amd64.zip           # Binary + DLLs zip
codeeagle-windows-arm64.zip
codeeagle_1.4.0_amd64.deb             # Debian/Ubuntu package
codeeagle_1.4.0_arm64.deb
codeeagle-1.4.0-1.x86_64.rpm          # Fedora/RHEL package
codeeagle-1.4.0-1.aarch64.rpm
codeeagle-windows-amd64-setup.exe     # Windows NSIS installer
codeeagle-windows-arm64-setup.exe
checksums.txt                          # SHA256 checksums
```

### Phase 4: Dev Release (Nightly) Updates

Update `release-dev.yml` to match the same build matrix:
- Same platforms: Linux (amd64/arm64), macOS (amd64/arm64), Windows (amd64/arm64)
- Same build tags: `faces app desktop production`
- Same package formats: tarballs, `.deb`, `.rpm`, `.zip`, NSIS installers
- Tag as `dev` pre-release (existing behavior — delete-and-recreate on each push to main)
- **Skip** WinGet submission (dev builds not submitted to registries)
- **Skip** Homebrew formula update (dev builds not submitted to taps)
- All artifacts still uploaded to the `dev` GitHub Release for `codeeagle update` auto-update

### Phase 5: Auto-Update for Windows

Update `internal/cli/update.go` to support Windows:

1. **`buildDownloadURL()`** — return `.zip` URL on Windows:
   ```go
   if osName == "windows" {
       assetName = fmt.Sprintf("codeeagle-%s-%s.zip", osName, arch)
   }
   ```

2. **`downloadAndInstall()`** — detect archive format and dispatch:
   ```go
   if strings.HasSuffix(downloadURL, ".zip") {
       extractZip(archivePath, tmpDir, "codeeagle.exe")
   } else {
       extractTarGz(archivePath, tmpDir, "codeeagle")
   }
   ```

3. **`extractZip()`** — new function using `archive/zip` stdlib.

4. **`replaceBinary()`** — already works on Windows (rename + copy strategy).

## Dependencies & Native Libraries

### Linux

```bash
# Build deps
sudo apt-get install -y \
  libopencv-dev \
  libwebkit2gtk-4.1-dev \
  nodejs npm \
  nfpm

# Runtime deps (declared in .deb/.rpm)
# libopencv-dev, libwebkit2gtk-4.1-dev
```

### macOS

```bash
# Build deps
brew install opencv node

# Runtime deps (declared in Homebrew formula)
# opencv
# WebKit is built into macOS
```

### Windows

```powershell
# Build deps
choco install opencv nsis nodejs -y

# Runtime deps (bundled in installer)
# opencv_world*.dll — bundled
# WebView2 — auto-installed by Wails at first launch
```

## Secrets Required

| Secret | Purpose | Used By |
|--------|---------|---------|
| `GITHUB_TOKEN` | Release creation, artifact upload | All release jobs (auto-provided) |
| `WINGET_PAT` | Submit manifests to `microsoft/winget-pkgs` | `update-winget` job |

`WINGET_PAT` is a GitHub PAT with `public_repo` scope for the `microsoft/winget-pkgs` fork.

## OpenCV Cross-Compilation Notes

### Linux arm64 (Appendix A)

Cross-compiling with OpenCV for arm64 on an amd64 runner requires:
- `gcc-aarch64-linux-gnu` cross-compiler (already used today)
- Pre-built arm64 OpenCV libraries OR build OpenCV from source with the cross-compiler
- Recommended: Use a Docker container with arm64 OpenCV pre-installed, or use `qemu-user-static` with an arm64 rootfs

Alternative: Use `ubuntu-22.04-arm` runner (GitHub now offers native ARM runners in larger plans).

### Windows arm64 (Appendix B)

Cross-compiling Go for Windows arm64 on an amd64 runner:
- Go supports `GOOS=windows GOARCH=arm64` cross-compilation
- OpenCV pre-built ARM64 binaries are available from the official release
- CGO cross-compilation requires `llvm-mingw` toolchain

Alternative: Skip Windows arm64 initially (very low market share), add later when demand exists.

## Phased Rollout

1. **Phase 1** — Add Windows amd64 builds (zip + NSIS installer) to release workflow. Add all build tags to existing Linux/macOS jobs. Create `nfpm.yaml` and produce `.deb`/`.rpm` for Linux amd64. Update `update.go` to handle `.zip` extraction on Windows.
2. **Phase 2** — Add Homebrew formula in `Formula/codeeagle.rb`. Add `update-homebrew` job to release workflow.
3. **Phase 3** — Add WinGet manifest submission. Create initial manifest in `microsoft/winget-pkgs`, add `update-winget` job.
4. **Phase 4** — Update `release-dev.yml` nightly to produce the same platform matrix and package formats as the stable release workflow (minus registry submissions).
5. **Phase 5** — Add arm64 builds for all platforms (Linux arm64 with native runner or Docker, Windows arm64 with llvm-mingw).

## Verification

```bash
# Linux (.deb)
sudo dpkg -i codeeagle_1.4.0_amd64.deb
codeeagle version
codeeagle status

# macOS (Homebrew)
brew tap imyousuf/codeeagle https://github.com/imyousuf/CodeEagle
brew install codeeagle
codeeagle version

# Windows (WinGet)
winget install imyousuf.CodeEagle
codeeagle version

# All platforms: verify features
codeeagle app          # Desktop UI launches
codeeagle rag "test"   # Vector search works
```
