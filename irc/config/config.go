// Package config provides unified TOML configuration loading for go-irc.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"golang.org/x/crypto/bcrypt"
)

// Config is the root configuration structure for go-irc.
type Config struct {
	Server  *ServerConfig  `toml:"server"`
	Bouncer *BouncerConfig `toml:"bouncer"`
}

// ServerConfig holds ircd configuration.
type ServerConfig struct {
	Name         string          `toml:"name"`
	Network      string          `toml:"network"`
	Listen       string          `toml:"listen"`
	TLSListen    string          `toml:"tls_listen"`
	TLSCertFile  string          `toml:"tls_cert_file"`
	TLSKeyFile   string          `toml:"tls_key_file"`
	MOTD         string          `toml:"motd"`
	MaxClients   int             `toml:"max_clients"`
	Password     string          `toml:"password"`
	PingInterval time.Duration   `toml:"ping_interval"`
	PingTimeout  time.Duration   `toml:"ping_timeout"`
	Opers        []OperConfig    `toml:"oper"`
	Accounts     []AccountConfig `toml:"account"`
	Caps         []string        `toml:"caps"`
	STS          *STSConfig      `toml:"sts"`
}

// AccountConfig describes an account available through SASL PLAIN.
type AccountConfig struct {
	Name     string `toml:"name"`
	Password string `toml:"password"` // bcrypt hash
	Host     string `toml:"host"`     // optional vhost
}

// STSConfig controls the strict transport security policy.
type STSConfig struct {
	Port      int           `toml:"port"`
	Duration  time.Duration `toml:"duration"`
	Preload   bool          `toml:"preload"`
	Hostnames []string      `toml:"hostnames"`
}

// OperConfig describes a server operator.
type OperConfig struct {
	Name     string `toml:"name"`
	Password string `toml:"password"` // bcrypt hash
	Host     string `toml:"host"`     // hostmask to allow
}

// BouncerConfig holds bouncer configuration.
type BouncerConfig struct {
	Listen    string          `toml:"listen"`
	TLSListen string          `toml:"tls_listen"`
	TLSCert   string          `toml:"tls_cert"`
	TLSKey    string          `toml:"tls_key"`
	Users     []UserConfig    `toml:"user"`
	Networks  []NetworkConfig `toml:"network"`
	History   HistoryConfig   `toml:"history"`
}

// UserConfig describes a bouncer user.
type UserConfig struct {
	Name     string `toml:"name"`
	Password string `toml:"password"`
}

// NetworkConfig describes an upstream IRC network.
type NetworkConfig struct {
	Name        string   `toml:"name"`
	Server      string   `toml:"server"`
	TLS         bool     `toml:"tls"`
	Nick        string   `toml:"nick"`
	User        string   `toml:"user"`
	RealName    string   `toml:"realname"`
	Password    string   `toml:"password"`
	SASLUser    string   `toml:"sasl_user"`
	SASLPass    string   `toml:"sasl_pass"`
	SASLMech    string   `toml:"sasl_mechanism"`
	Channels    []string `toml:"channels"`
	AutoConnect bool     `toml:"auto_connect"`
}

// HistoryConfig controls history storage.
type HistoryConfig struct {
	Backend string `toml:"backend"` // "memory" or "sqlite"
	DSN     string `toml:"dsn"`
	Limit   int    `toml:"limit"`
}

// Load reads a TOML config file from path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %q: %w", path, err)
	}
	var cfg Config
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %q: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config: validate: %w", err)
	}
	cfg.applyDefaults()
	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Server == nil && c.Bouncer == nil {
		return fmt.Errorf("at least one of [server] or [bouncer] must be configured")
	}
	if c.Server != nil {
		s := c.Server
		if (s.TLSCertFile == "") != (s.TLSKeyFile == "") {
			return fmt.Errorf("tls_cert_file and tls_key_file must be configured together")
		}
		seen := make(map[string]bool)
		passwordCost := -1
		for _, account := range s.Accounts {
			key := strings.ToLower(account.Name)
			if account.Name == "" || account.Name == "*" || strings.ContainsAny(account.Name, " \x00\r\n,:!") {
				return fmt.Errorf("invalid account name %q", account.Name)
			}
			if seen[key] {
				return fmt.Errorf("duplicate account %q", account.Name)
			}
			seen[key] = true
			cost, err := bcrypt.Cost([]byte(account.Password))
			if err != nil {
				return fmt.Errorf("invalid password hash for account %q", account.Name)
			}
			if passwordCost < 0 {
				passwordCost = cost
			} else if cost != passwordCost {
				return fmt.Errorf("account password hashes must use the same bcrypt cost")
			}
			if len(account.Host) > 253 || strings.ContainsAny(account.Host, " \x00\r\n!@:") {
				return fmt.Errorf("invalid host for account %q", account.Name)
			}
		}
		if s.STS != nil {
			if s.STS.Port < 1 || s.STS.Port > 65535 || s.STS.Duration < 0 || s.STS.Preload && s.STS.Duration == 0 {
				return fmt.Errorf("invalid STS policy")
			}
			if s.TLSListen == "" || s.TLSCertFile == "" || len(s.STS.Hostnames) == 0 {
				return fmt.Errorf("STS requires TLS and at least one hostname")
			}
		}
	}
	if c.Bouncer != nil {
		for _, network := range c.Bouncer.Networks {
			if (network.SASLUser == "") != (network.SASLPass == "") {
				return fmt.Errorf("network %q requires both sasl_user and sasl_pass", network.Name)
			}
			if network.SASLUser != "" && !network.TLS {
				return fmt.Errorf("network %q requires TLS for SASL PLAIN", network.Name)
			}
			if network.SASLMech != "" && !strings.EqualFold(network.SASLMech, "PLAIN") {
				return fmt.Errorf("network %q has unsupported SASL mechanism %q", network.Name, network.SASLMech)
			}
		}
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.Server != nil {
		s := c.Server
		if s.Name == "" {
			s.Name = "irc.local"
		}
		if s.Network == "" {
			s.Network = "LocalNet"
		}
		if s.Listen == "" {
			s.Listen = ":6667"
		}
		if s.PingInterval == 0 {
			s.PingInterval = 90 * time.Second
		}
		if s.PingTimeout == 0 {
			s.PingTimeout = 30 * time.Second
		}
	}
	if c.Bouncer != nil {
		b := c.Bouncer
		if b.Listen == "" {
			b.Listen = ":6668"
		}
		if b.History.Limit == 0 {
			b.History.Limit = 500
		}
		if b.History.Backend == "" {
			b.History.Backend = "memory"
		}
		for i := range b.Networks {
			n := &b.Networks[i]
			if n.Nick == "" {
				n.Nick = "gopher"
			}
			if n.User == "" {
				n.User = "gopher"
			}
			if n.RealName == "" {
				n.RealName = "Gopher via go-irc bouncer"
			}
		}
	}
}
