//go:build linux

package config

type TailscaleConfig struct {
	Enabled           bool                  `toml:"enabled"`
	Backend           string                `toml:"backend"`
	CLIPath           string                `toml:"cli_path"`
	SSHPath           string                `toml:"ssh_path"`
	CommandTimeout    string                `toml:"command_timeout"`
	SSHCommandTimeout string                `toml:"ssh_command_timeout"`
	ExpectedTailnet   string                `toml:"expected_tailnet"`
	ExpectedHostname  string                `toml:"expected_hostname"`
	ExpectedTags      []string              `toml:"expected_tags"`
	Parent            TailscaleParentConfig `toml:"parent"`
}

type TailscaleParentConfig struct {
	Enabled         bool     `toml:"enabled"`
	Hostname        string   `toml:"hostname"`
	StateDir        string   `toml:"state_dir"`
	ListenAddr      string   `toml:"listen_addr"`
	AuthKeyEnv      string   `toml:"auth_key_env"`
	AuthKeyFile     string   `toml:"auth_key_file"`
	Tags            []string `toml:"tags"`
	AdminLoginNames []string `toml:"admin_login_names"`
}

type GitHubConfig struct {
	Enabled    bool              `toml:"enabled"`
	APIBaseURL string            `toml:"api_base_url"`
	APIVersion string            `toml:"api_version"`
	Apps       []GitHubAppConfig `toml:"apps"`
}

type GitHubAppConfig struct {
	Name                 string   `toml:"name"`
	AppID                int64    `toml:"app_id"`
	InstallationID       int64    `toml:"installation_id"`
	PrivateKeyFile       string   `toml:"private_key_file"`
	Repositories         []string `toml:"repositories"`
	Permissions          []string `toml:"permissions"`
	AllowAllRepositories bool     `toml:"allow_all_repositories"`
	AllowAllPermissions  bool     `toml:"allow_all_permissions"`
}
