package agent

type Backend string

const (
	BackendExternal Backend = "external"
	BackendBuiltin  Backend = "builtin"
)

type Definition struct {
	Name                     string
	Backend                  Backend
	BuiltinAvailable         bool
	BuiltinHookFile          string
	PluginName               string
	AgentCommand             string
	SupportedPlatforms       []string
	WindowsInstaller         string
	PackageScript            string
	PackageArgs              []string
	PackageRootArg           bool
	DiscoveryCommandOptional bool
	Env                      []string
	InstallArgs              []string
	WindowsArgs              []string
	Markers                  []string
	ConfigFiles              []string
	EnabledJSONPath          []string
	RemoveCmds               [][]string
	RemovePaths              []string
	Hidden                   bool
	Resolve                  func(Definition) Definition
	ResolveInstall           func(Definition) (Definition, error)
	ResolveDiscovery         func(Definition) (Definition, bool)
}

func (d Definition) IsBuiltin() bool {
	return d.Backend == BackendBuiltin
}

func (d Definition) WithBuiltin() (Definition, bool) {
	if !d.BuiltinAvailable || d.BuiltinHookFile == "" {
		return d, false
	}
	d.Backend = BackendBuiltin
	d.PluginName = "obs-agent-connector"
	d.PackageScript = ""
	d.PackageArgs = nil
	d.PackageRootArg = false
	d.Markers = append([]string{d.BuiltinHookFile}, d.Markers...)
	d.RemoveCmds = nil
	d.RemovePaths = nil
	return d, true
}

type Candidate struct {
	Plugin           Definition
	DetectedCmd      string
	InstalledPath    string
	InstalledVersion string
	Supported        bool
}
