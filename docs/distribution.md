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
- `install-obs-agent-connector.sh`
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
3. Renders release notes from `docs/release-template.md`
4. Publishes the artifacts and generated notes to GitHub Releases

## Preferred Install Method

Use the installer script instead of opening the binary directly.
The installer:

- downloads the correct package for the current platform
- installs a wrapper command into a bin directory
- stores the real binary under `~/.obs-agent-connector/<brand>/bin/`
- writes `~/.obs-agent-connector/<brand>/config.json`
- keeps release/update metadata isolated per brand

Example:

```bash
curl -fsSL -O https://github.com/GuanceCloud/obs-agent-connector/releases/download/v0.1.1/install-obs-agent-connector.sh
sh install-obs-agent-connector.sh --version v0.1.1 --brand guance
```

For another brand, point the installer at a different release source:

```bash
sh install-obs-agent-connector.sh \
  --brand truewatch \
  --release-repo GuanceCloud/obs-agent-connector \
  --release-api-url https://api.github.com/repos/GuanceCloud/obs-agent-connector/releases/latest \
  --release-latest-url https://github.com/GuanceCloud/obs-agent-connector/releases/latest \
  --release-page-base-url https://github.com/GuanceCloud/obs-agent-connector/releases/tag \
  --release-download-base-url https://github.com/GuanceCloud/obs-agent-connector/releases/download
```

The generated config file contains the release/download endpoints used later by `version` and self-update commands.
The wrapper command exports `OBS_AGENT_CONNECTOR_CONFIG`, so different brands do not share release metadata accidentally.

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

## Windows Install Example

Unzip the package:

```powershell
Expand-Archive .\obs-agent-connector-windows-amd64.zip -DestinationPath .
```

Run:

```powershell
.\obs-agent-connector-windows-amd64.exe doctor
```

You can optionally rename the executable to `obs-agent-connector.exe` and add its directory to `PATH`.
