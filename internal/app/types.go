package app

type installInput struct {
	Endpoint       string
	TracePath      string
	MetricsPath    string
	XToken         string
	Headers        []string
	AgentID        string
	AgentName      string
	GlobalTags     []string
	CaptureContent string
	MaxChars       int
	Enabled        *bool
}

type githubRelease struct {
	TagName string
	HTMLURL string
}

type connectorConfig struct {
	DownloadBaseURL string            `json:"download_base_url"`
	PluginSource    string            `json:"plugin_source"`
	PluginBaseURL   string            `json:"plugin_base_url"`
	Endpoint        string            `json:"endpoint"`
	XToken          string            `json:"x_token"`
	GlobalTags      []string          `json:"global_tags"`
	TracePath       string            `json:"trace_path"`
	MetricsPath     string            `json:"metrics_path"`
	Headers         map[string]string `json:"headers"`
	CaptureContent  string            `json:"capture_content"`
	MaxChars        int               `json:"max_chars"`
	Enabled         *bool             `json:"enabled"`
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
