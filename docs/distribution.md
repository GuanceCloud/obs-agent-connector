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

## macOS Install Example

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
