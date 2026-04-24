# Installing agentctl

## Build from clone (recommended)

```bash
git clone https://github.com/arun-gupta/agentctl
cd agentctl
go build -o agentctl ./cmd/agentctl
# Run from this directory so ./agents/ sits next to ./agentctl
./agentctl --help
```

To install elsewhere, keep **`agentctl` and `agents/` in the same directory** (for example copy both into `/opt/agentctl/` and put that directory on your `PATH`, or run from the clone as above).

## Prebuilt binaries — GitHub Releases (stable)

Tagged releases publish archives for all supported platforms. Download the archive for your OS/arch, extract it, and add the `agentctl/` directory to your `PATH`.

**macOS / Linux**

```bash
# Replace <os>-<arch> with your platform:
# linux-amd64 | linux-arm64 | darwin-amd64 | darwin-arm64
curl -fsSL https://github.com/arun-gupta/agentctl/releases/latest/download/agentctl-<os>-<arch>.tar.gz \
  | tar -xz
sudo mv agentctl /usr/local/bin/agentctl   # or any directory on your PATH
agentctl version
```

**Windows (PowerShell)**

```powershell
# Download from the Releases page:
# https://github.com/arun-gupta/agentctl/releases/latest
# Then extract and move agentctl.exe to a directory on your PATH.
Expand-Archive agentctl-windows-amd64.zip -DestinationPath .
.\agentctl\agentctl.exe version
```

> **Note:** The archive contains both the `agentctl` binary and the `agents/` adapter scripts.  
> Keep both in the same directory (e.g. `/opt/agentctl/`) and add that directory to your `PATH`.

## Prebuilt binaries — per-commit snapshots

Every push to `main` runs the [`snapshot` workflow](../.github/workflows/snapshot.yml) which publishes
workflow artifacts for the full platform matrix (14-day retention). Use these to test unreleased builds.

1. Go to **[Actions → snapshot](https://github.com/arun-gupta/agentctl/actions/workflows/snapshot.yml)**.
2. Open the latest successful run on `main`.
3. Download the artifact for your platform, e.g. `agentctl-<sha>-linux-amd64` (`.tar.gz`) or `agentctl-<sha>-windows-amd64` (`.zip`).
4. Extract and place `agentctl` (or `agentctl.exe`) + the `agents/` directory in the same folder on your `PATH`.

Artifact naming: `agentctl-<7-char-sha>-<goos>-<goarch>`, e.g. `agentctl-a1b2c3d-linux-amd64.tar.gz`.

## Additional install options

For symlink-based installs and git subtree vendoring, see [development.md](development.md#install-instructions).
