# DeepSeek Harness Integration

The connector integrates the released [`dsh-otel-plugin`](https://github.com/GuanceCloud/dsh-otel-plugin) as an external bundle. DSH profiles live below `$DSH_HOME/profiles/<profile>` and the plugin reads its managed runtime configuration from `$DSH_HOME/gtrace.json` (default `~/.dsh`).

Discovery accepts either a `dsh` executable in `PATH` or an existing DSH home. The connector defaults to the `web` profile and honors `DSH_HOME` and `DSH_PROFILE`. Unix installation downloads the published tarball and runs `scripts/install-release.sh`; Windows uses the published PowerShell installer.

The connector writes the plugin's `gtrace.json` only after the bundle installation succeeds, while updates invoke the same installer with configuration preserved. This keeps endpoint, headers, resource attributes, content capture, and enabled state consistent with the connector's other external integrations.

