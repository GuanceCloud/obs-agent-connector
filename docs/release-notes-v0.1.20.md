# obs-agent-connector v0.1.20

## Highlights

- Adds DeepSeek Harness (`dsh`) as an external OTEL plugin integration.
- Standardizes external plugin installation and upgrade parameters across OSS and GitHub Release sources.
- Keeps plugin-owned runtime configuration in the plugin installer instead of connector-specific configuration code.

## Installation and lifecycle

- Passes the common `latest`, `--type gtrace`, endpoint, X-Token, tags, and profile parameters to external installers.
- Generates valid `agid_<32-hex>` agent identities and rejects invalid explicit identities.
- External plugin installs no longer read or overwrite Agent-private runtime configuration.
- Adds automatic package-manager fallback for external plugin removal when the native Agent CLI is unavailable.
- Refreshes GitHub `latest` installer assets to avoid stale release-script downloads.

## Reliability

- Adds regression coverage for the external installer contract, DSH URLs, argument generation, identity handling, and runtime-config ownership.
- Preserves existing plugin behavior while keeping endpoint, token, tags, and profile handling consistent across platforms.

## Platforms and artifacts

- Release packages are provided for macOS, Linux, and Windows.
- Includes installer scripts and `SHA256SUMS` checksums.
