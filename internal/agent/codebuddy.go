package agent

import "os/exec"

func codeBuddyPlugin() Definition {
	return Definition{
		Name:                     "codebuddy",
		Backend:                  BackendBuiltin,
		BuiltinHookFile:          "~/.codebuddy/settings.json",
		PluginName:               "obs-agent-connector",
		AgentCommand:             "codebuddy",
		SupportedPlatforms:       []string{"darwin", "linux", "windows"},
		DiscoveryCommandOptional: true,
		Markers:                  []string{"~/.codebuddy/settings.json"},
		ConfigFiles:              []string{"~/.codebuddy/gtrace.json"},
		EnabledJSONPath:          []string{"enabled"},
		ResolveDiscovery: func(definition Definition) (Definition, bool) {
			if PathExists(ExpandHome("~/.codebuddy")) {
				return definition, true
			}
			_, err := exec.LookPath(definition.AgentCommand)
			return definition, err == nil
		},
	}
}
