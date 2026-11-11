// Package config loads and saves the bootstrap configuration file.
//
// Only what the service needs before it can reach the database lives here
// (where to listen, the access token, the database address, folders).
// Everything that can change at runtime (approved list, send rules,
// attachment folder) lives in the database so it can be edited by hand or
// from Claude and every change is audited. See package policy.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ManagedPostgres describes the private PostgreSQL instance that the
// installer creates only for WhatsApp. When nil, DatabaseURL points to a
// server that is managed elsewhere.
type ManagedPostgres struct {
	BinDir            string `json:"bin_dir"`
	DataDir           string `json:"data_dir"`
	Port              int    `json:"port"`
	ServiceName       string `json:"service_name"`
	SuperuserPassword string `json:"superuser_password"`
	AppUser           string `json:"app_user"`
	AppPassword       string `json:"app_password"`
	Database          string `json:"database"`
}

// Config is the bootstrap configuration.
type Config struct {
	// ListenPort is the TCP port for MCP (/mcp) and the REST API (/api).
	ListenPort int `json:"listen_port"`
	// ListenHosts are the addresses to bind. The special value "tailscale"
	// means "every Tailscale address of this machine" (100.64.0.0/10 and
	// fd7a:115c:a1e0::/48), picked up automatically when Tailscale comes up.
	// Wildcard addresses (0.0.0.0, ::) are refused on purpose.
	ListenHosts []string `json:"listen_hosts"`
	// AllowedNetworks: client addresses outside these ranges are rejected
	// before the token is even checked.
	AllowedNetworks []string `json:"allowed_networks"`
	// AuthToken is required on every request (Authorization: Bearer ...).
	AuthToken string `json:"auth_token"`
	// RESTEnabled keeps the old /api/send and /api/download endpoints
	// (same token and same rules as MCP) for other apps such as LeadsApp.
	RESTEnabled bool `json:"rest_enabled"`

	DatabaseURL     string           `json:"database_url"`
	ManagedPostgres *ManagedPostgres `json:"managed_postgres,omitempty"`

	// DataDir holds downloads and logs.
	DataDir  string `json:"data_dir"`
	MediaDir string `json:"media_dir"`
	LogFile  string `json:"log_file"`
	LogLevel string `json:"log_level"`

	// FFmpegPath is used to convert audio to Opus for voice notes. Empty
	// means "look for ffmpeg in PATH".
	FFmpegPath string `json:"ffmpeg_path"`

	// DeviceName is how this connector shows up in WhatsApp > Linked devices.
	DeviceName string `json:"device_name"`

	// Cloud access (Tailscale Funnel). The connector listens on
	// 127.0.0.1:CloudPort for MCP only, answers only while the
	// cloud_access_enabled setting is on, and only with CloudKey (in the
	// address /mcp/k/<key> or as a Bearer token). Funnel publishes that
	// port on https://<machine>.<tailnet>.ts.net:CloudPublicPort.
	CloudPort       int    `json:"cloud_port"`
	CloudPublicPort int    `json:"cloud_public_port"`
	CloudKey        string `json:"cloud_key"`

	// Own Tailscale machine for cloud access (2.0.5): WhatsApp MCP joins
	// the tailnet as CloudNodeHostname and publishes MCP with Funnel on
	// port 443, apart from the server's own Tailscale. Its state lives in
	// DataDir/tsnet.
	CloudNodeEnabled  bool   `json:"cloud_node_enabled"`
	CloudNodeHostname string `json:"cloud_node_hostname"`

	path string
}

// ProgramDataDir returns the default data folder for this OS.
func ProgramDataDir() string {
	if runtime.GOOS == "windows" {
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "WhatsAppMCP")
	}
	if d := os.Getenv("WHATSAPP_MCP_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".whatsapp-mcp")
}

// DefaultPath is where the config file lives unless overridden.
func DefaultPath() string {
	if p := os.Getenv("WHATSAPP_MCP_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(ProgramDataDir(), "config.json")
}

// Defaults returns a config with safe defaults and fresh secrets.
func Defaults(dataDir string) (*Config, error) {
	tok, err := RandomSecret(32)
	if err != nil {
		return nil, err
	}
	cloud, err := RandomSecret(24)
	if err != nil {
		return nil, err
	}
	return &Config{
		ListenPort:      8765,
		ListenHosts:     []string{"127.0.0.1", "tailscale"},
		AllowedNetworks: []string{"127.0.0.0/8", "::1/128", "100.64.0.0/10", "fd7a:115c:a1e0::/48"},
		AuthToken:       tok,
		RESTEnabled:     true,
		DataDir:         dataDir,
		MediaDir:        filepath.Join(dataDir, "media"),
		LogFile:         filepath.Join(dataDir, "logs", "whatsapp-mcp.log"),
		LogLevel:        "INFO",
		DeviceName:      "WhatsApp MCP (server)",
		CloudPort:       8767,
		CloudPublicPort: 10000,
		CloudKey:        cloud,

		CloudNodeHostname: DefaultNodeHostname,
	}, nil
}

// RandomSecret returns n random bytes hex-encoded.
func RandomSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Load reads and validates the config file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	c.path = path
	c.fillNew()
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return c, nil
}

// fillNew sets defaults for fields added after the file was written.
func (c *Config) fillNew() {
	if c.CloudPort == 0 {
		c.CloudPort = 8767
	}
	if c.CloudPublicPort == 0 {
		c.CloudPublicPort = 10000
	}
	if c.CloudNodeHostname == "" {
		c.CloudNodeHostname = DefaultNodeHostname
	}
}

// DefaultNodeHostname is the Tailscale name of the cloud machine.
const DefaultNodeHostname = "whatsapp-mcp"

// ValidNodeHostname accepts a Tailscale machine name: lowercase letters,
// digits and dashes, 1 to 63 characters, not starting or ending with a dash.
func ValidNodeHostname(h string) bool {
	if len(h) == 0 || len(h) > 63 || h[0] == '-' || h[len(h)-1] == '-' {
		return false
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// NodeDir is where the cloud machine keeps its Tailscale state.
func (c *Config) NodeDir() string { return filepath.Join(c.DataDir, "tsnet") }

// EnsureCloudKey makes a cloud key if there is none yet (configs from
// 2.0.0) and reports whether the config changed.
func (c *Config) EnsureCloudKey() (bool, error) {
	if c.CloudKey != "" {
		return false, nil
	}
	k, err := RandomSecret(24)
	if err != nil {
		return false, err
	}
	c.CloudKey = k
	return true, nil
}

// Path returns the file the config was loaded from.
func (c *Config) Path() string { return c.path }

// Save writes the config with owner-only permissions (on Windows the
// installer additionally restricts the folder ACL to Administrators/SYSTEM).
func (c *Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	c.path = path
	return os.Rename(tmp, path)
}

// Validate refuses unsafe or incomplete configurations.
func (c *Config) Validate() error {
	if c.ListenPort <= 0 || c.ListenPort > 65535 {
		return errors.New("listen_port must be 1-65535")
	}
	if len(c.ListenHosts) == 0 {
		return errors.New("listen_hosts is empty")
	}
	for _, h := range c.ListenHosts {
		h = strings.TrimSpace(h)
		if strings.EqualFold(h, "tailscale") {
			continue
		}
		ip := net.ParseIP(h)
		if ip == nil {
			return fmt.Errorf("listen_hosts: %q is not an IP address or \"tailscale\"", h)
		}
		if ip.IsUnspecified() {
			return fmt.Errorf("listen_hosts: %q would open the port to every network; use 127.0.0.1 and/or tailscale", h)
		}
	}
	if len(c.AllowedNetworks) == 0 {
		return errors.New("allowed_networks is empty")
	}
	for _, n := range c.AllowedNetworks {
		if _, _, err := net.ParseCIDR(n); err != nil {
			return fmt.Errorf("allowed_networks: %q: %w", n, err)
		}
	}
	if len(c.AuthToken) < 32 {
		return errors.New("auth_token must be at least 32 characters")
	}
	if c.DatabaseURL == "" {
		return errors.New("database_url is empty")
	}
	if c.MediaDir == "" {
		return errors.New("media_dir is empty")
	}
	if c.CloudPort < 0 || c.CloudPort > 65535 || (c.CloudPort != 0 && c.CloudPort == c.ListenPort) {
		return errors.New("cloud_port must be 1-65535 and different from listen_port")
	}
	if c.CloudKey != "" && len(c.CloudKey) < 32 {
		return errors.New("cloud_key must be at least 32 characters")
	}
	if c.CloudNodeHostname != "" && !ValidNodeHostname(c.CloudNodeHostname) {
		return errors.New("cloud_node_hostname must be lowercase letters, digits and dashes (1-63)")
	}
	return nil
}
