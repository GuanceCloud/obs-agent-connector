package agent

func hermesPlugin() Definition {
	return Definition{
		Name:         "hermes",
		PluginName:   "hermes-otel-plugin",
		AgentCommand: "hermes",
		Markers: []string{
			"~/.hermes/plugins/hermes-otel-plugin",
		},
		ConfigFiles: []string{"~/.hermes/config.yaml"},
		RemovePaths: []string{
			"~/.hermes/plugins/hermes-otel-plugin",
		},
	}
}
