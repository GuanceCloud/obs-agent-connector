# Distribution

Release artifacts are written to `dist/`.

## Build All Release Packages

```bash
./scripts/build-release.sh
```

The script builds:

| Platform | Architecture | Package |
| --- | --- | --- |
| macOS | Apple Silicon | `obs-agent-connector-darwin-arm64.tar.gz` |
| macOS | Intel | `obs-agent-connector-darwin-amd64.tar.gz` |
| Linux | x86_64 | `obs-agent-connector-linux-amd64.tar.gz` |
| Linux | ARM64 | `obs-agent-connector-linux-arm64.tar.gz` |
| Windows | x86_64 | `obs-agent-connector-windows-amd64.zip` |
| Windows | ARM64 | `obs-agent-connector-windows-arm64.zip` |

The script also writes `dist/SHA256SUMS`.
If `VERSION` is set, the script embeds that value into the built binaries.

## Build with GitHub Actions

Use the `Package` workflow when you want GitHub Actions to produce release packages without publishing a GitHub Release.

1. Open `Actions` in the GitHub repository.
2. Select the `Package` workflow.
3. Run the workflow.
4. Optionally set the `version` input, such as `v0.1.1`.
5. Download the generated artifact from the workflow run summary.

The workflow uploads:

- macOS tarballs
- Linux tarballs
- Windows zip packages
- `install.sh`
- `install.ps1`
- `latest.txt`
- `SHA256SUMS`

## Publish a GitHub Release

Use a Git tag to publish a release from the same packaging workflow definition.

```bash
git tag v0.1.1
git push origin v0.1.1
```

The `Release` workflow:

1. Calls the reusable `Package` workflow with the tag name as `version`
2. Downloads the packaged artifacts
3. Renders release notes directly from commit subjects between the previous tag and the current tag
4. Publishes the artifacts and generated notes to GitHub Releases

When mirroring a release to a CDN-backed static download directory, upload the complete artifact set before updating `latest.txt` and `SHA256SUMS`. Purge or use a short cache policy for the stable `install.sh`, `install.ps1`, and `latest.txt` URLs. Package downloads include the resolved version as a cache key to avoid stale unversioned artifacts.

## Preferred Install Method

Use the installer script instead of opening the binary directly.
The installer:

- downloads the correct package for the current platform
- derives the download source from the endpoint when no source is supplied
- verifies the package against `SHA256SUMS` before extraction
- installs `obs-agent-connector` into a bin directory
- writes `~/.obs-agent-connector/config.json`
- records the CLI download source plus shared `endpoint` and `x_token` defaults for later `discover`, `install`, `version`, and self-update operations

Example:

```bash
curl -fsSL -O https://static.guance.com/obs-agent-connector/install.sh
sh install.sh --endpoint=https://llm-openway.guance.com --x-token=agent_xxx
```

If the installer adds `~/.local/bin` to your shell profile, reload the profile before using the command in the current shell:

```bash
source ~/.zshrc
```

If you want to install a specific version:

```bash
curl -fsSL -O https://static.guance.com/obs-agent-connector/install.sh
sh install.sh \
  --version v0.1.4 \
  --endpoint=https://llm-openway.guance.com \
  --x-token=agent_xxx
```

You can still pass the source explicitly:

```bash
sh install.sh \
  --download-base-url <download-base-url> \
  --endpoint <endpoint> \
  --x-token <token>
```

## Windows Preferred Install Method

Use the PowerShell installer on Windows.
The installer:

- downloads the correct Windows zip package
- installs `obs-agent-connector.exe` into a user-local bin directory
- writes `%USERPROFILE%\.obs-agent-connector\config.json`
- updates the user `PATH` by default

Example:

```powershell
$env:OBS_AGENT_CONNECTOR_OSS_ENDPOINT = "https://static.guance.com/obs-agent-connector"
Invoke-WebRequest -Uri "$env:OBS_AGENT_CONNECTOR_OSS_ENDPOINT/install.ps1" -OutFile "install.ps1"
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Endpoint "https://llm-openway.guance.com" -XToken "agent_xxx"
```

If you want to install a specific version:

```powershell
$env:OBS_AGENT_CONNECTOR_OSS_ENDPOINT = "https://static.guance.com/obs-agent-connector"
Invoke-WebRequest -Uri "$env:OBS_AGENT_CONNECTOR_OSS_ENDPOINT/install.ps1" -OutFile "install.ps1"
powershell -ExecutionPolicy Bypass -File .\install.ps1 -Version v0.1.4 -Endpoint "https://llm-openway.guance.com" -XToken "agent_xxx"
```

You can still pass the source explicitly:

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1 -DownloadBaseUrl <download-base-url> -Endpoint <endpoint> -XToken <token>
```

The generated config file contains the CLI download base URL plus shared `endpoint` and `x_token` values used later by `discover`, `install`, `version`, and self-update commands.
That download base should also expose `latest.txt`.

## macOS Manual Install Example

Do not double-click the extracted binary in Finder.
When Finder opens a command-line executable, macOS Terminal appends `; exit;` automatically. This is macOS behavior, not CLI output.
Run the binary from an existing Terminal session, or move it into `PATH` first.

Apple Silicon:

```bash
tar -xzf obs-agent-connector-darwin-arm64.tar.gz
chmod +x obs-agent-connector-darwin-arm64
sudo mv obs-agent-connector-darwin-arm64 /usr/local/bin/obs-agent-connector
```

Intel Mac:

```bash
tar -xzf obs-agent-connector-darwin-amd64.tar.gz
chmod +x obs-agent-connector-darwin-amd64
sudo mv obs-agent-connector-darwin-amd64 /usr/local/bin/obs-agent-connector
```

If macOS blocks the binary because of quarantine metadata:

```bash
xattr -d com.apple.quarantine /usr/local/bin/obs-agent-connector
```

Then run:

```bash
obs-agent-connector version
```

## Linux Install Example

```bash
tar -xzf obs-agent-connector-linux-amd64.tar.gz
chmod +x obs-agent-connector-linux-amd64
sudo mv obs-agent-connector-linux-amd64 /usr/local/bin/obs-agent-connector
```

For ARM64 Linux, use `obs-agent-connector-linux-arm64.tar.gz`.

## Windows Manual Install Example

If you do not want to use the installer, unzip the package:

```powershell
Expand-Archive .\obs-agent-connector-windows-amd64.zip -DestinationPath .
```

Verify the binary:

```powershell
.\obs-agent-connector-windows-amd64.exe version --offline
```

You can optionally rename the executable to `obs-agent-connector.exe`, place it in a stable directory, and add that directory to `PATH`.
