## Overview
{{APP_NAME}} {{VERSION}} is a packaged release for installing and managing OBS/GTrace plugins across multiple AI coding agents.

The CLI uses each official plugin installer and writes the final runtime configuration into the target Agent's own `gtrace.json`.

## Included in this release
- Interactive plugin installation with required OBS/GTrace parameters
- Plugin update for a single Agent without overwriting existing configuration
- Plugin removal, with optional configuration cleanup
- Local plugin inspection with `list`
- Environment and installation diagnostics with `doctor`
- CLI version check with GitHub release update detection
- Cross-platform packages for macOS, Linux, and Windows

## Supported Agents
- claude
- codex
- hermes
- openclaw
- qoder

## Compatibility
- `qoder-cn` is still accepted as a legacy compatibility target and always forces the CN layout

## Download
Choose the package that matches your operating system and CPU architecture:
- macOS: `darwin-amd64` or `darwin-arm64`
- Linux: `linux-amd64` or `linux-arm64`
- Windows: `windows-amd64` or `windows-arm64`

Use `SHA256SUMS` to verify the downloaded package.

## Quick start
After extracting the package, run:
`{{APP_NAME}} version`

Then install a plugin, for example:
`{{APP_NAME}} install codex`

## Documentation
- README: https://github.com/GuanceCloud/obs-agent-connector#readme
- Commands: https://github.com/GuanceCloud/obs-agent-connector/blob/main/docs/commands.md
- Distribution: https://github.com/GuanceCloud/obs-agent-connector/blob/main/docs/distribution.md
