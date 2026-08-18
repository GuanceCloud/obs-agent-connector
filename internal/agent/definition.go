package agent

type Backend string

const (
	BackendExternal Backend = "external"
	BackendBuiltin  Backend = "builtin"
)

type Definition struct {
	Name                     string
	Backend                  Backend
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
	RemoveFallbackCmd        []string
	RemovePaths              []string
	RemoveCleanupDetails     []string
	RemoveCleanup            func(Definition) error
	Hidden                   bool
	Resolve                  func(Definition) Definition
	ResolveInstall           func(Definition) (Definition, error)
	ResolveRemove            func(Definition) Definition
	ResolveDiscovery         func(Definition) (Definition, bool)
}

func (d Definition) IsBuiltin() bool {
	return d.Backend == BackendBuiltin
}

type Candidate struct {
	Plugin            Definition
	DetectedCmd       string
	InstalledPath     string
	InstalledVersion  string
	Supported         bool
	UnsupportedReason string
}
