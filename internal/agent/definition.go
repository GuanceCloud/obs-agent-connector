package agent

type Definition struct {
	Name             string
	PluginName       string
	AgentCommand     string
	WindowsInstaller string
	Env              []string
	InstallArgs      []string
	WindowsArgs      []string
	Markers          []string
	ConfigFiles      []string
	EnabledJSONPath  []string
	RemoveCmds       [][]string
	RemovePaths      []string
	Hidden           bool
	Resolve          func(Definition) Definition
	ResolveInstall   func(Definition) (Definition, error)
	ResolveDiscovery func(Definition) (Definition, bool)
}

type Candidate struct {
	Plugin        Definition
	DetectedCmd   string
	InstalledPath string
	Supported     bool
}
