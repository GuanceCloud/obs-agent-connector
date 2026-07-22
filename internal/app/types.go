package app

type installInput struct {
	Endpoint  string
	XToken    string
	AgentID   string
	AgentName string
}

type githubRelease struct {
	TagName string
	HTMLURL string
}

type connectorConfig struct {
	DownloadBaseURL string `json:"download_base_url"`
	PluginSource    string `json:"plugin_source"`
	PluginBaseURL   string `json:"plugin_base_url"`
	Endpoint        string `json:"endpoint"`
	XToken          string `json:"x_token"`
}

type pluginDownloadConfig struct {
	Source  string
	BaseURL string
}

type discoverResult struct {
	Agent  string
	Result string
	Detail string
}
