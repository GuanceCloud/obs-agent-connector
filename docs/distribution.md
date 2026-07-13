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
3. Renders release notes from `docs/release-template.md`
4. Publishes the artifacts and generated notes to GitHub Releases

## Preferred Install Method

Use the installer script instead of opening the binary directly.
The installer:

- downloads the correct package for the current platform
- installs `obs-agent-connector` into a bin directory
- writes `~/.obs-agent-connector/config.json`
- records the CLI download source for later `version` and self-update operations

Example:

```bash
curl -fsSL -O <download-base-url>/install.sh
sh install.sh --download-base-url <download-base-url>
```

If you want to install a specific version:

```bash
sh install.sh \
  --version v0.1.1 \
  --download-base-url <download-base-url>
```

The generated config file contains the CLI download base URL used later by `version` and self-update commands.
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
