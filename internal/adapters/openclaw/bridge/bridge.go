// Package bridge embeds the dependency-free native OpenClaw entry point.
package bridge

import _ "embed"

//go:embed index.mjs
var Source []byte

const PluginID = "obs-agent-connector"
