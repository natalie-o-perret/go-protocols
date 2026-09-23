// Command ircd is a full-featured IRC server.
//
// Usage:
//
//	ircd [-config path/to/config.toml]
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/natalie-o-perret/go-protocols/irc/config"
	"github.com/natalie-o-perret/go-protocols/irc/server"
)

// Build-time variables injected by goreleaser / go build -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cfgPath := flag.String("config", "ircd.toml", "Path to TOML config file")
	showVer := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVer {
		fmt.Printf("ircd %s (%s) built %s\n", version, commit, date)
		return
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		// Fall back to sensible defaults when no config file exists
		if os.IsNotExist(err) {
			slog.Warn("no config file found, using built-in defaults", "path", *cfgPath)
			runWithDefaults()
			return
		}
		fmt.Fprintf(os.Stderr, "ircd: %v\n", err)
		os.Exit(1)
	}

	if cfg.Server == nil {
		fmt.Fprintln(os.Stderr, "ircd: [server] section missing from config")
		os.Exit(1)
	}

	sc := cfg.Server
	opers := make(map[string]string, len(sc.Opers))
	for _, o := range sc.Opers {
		opers[o.Name] = o.Password
	}
	accounts := make(map[string]server.Account, len(sc.Accounts))
	for _, account := range sc.Accounts {
		accounts[account.Name] = server.Account{Name: account.Name, PasswordHash: account.Password, Host: account.Host}
	}
	var tlsConfig *tls.Config
	if sc.TLSCertFile != "" {
		certificate, err := tls.LoadX509KeyPair(sc.TLSCertFile, sc.TLSKeyFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ircd: load TLS certificate: %v\n", err)
			os.Exit(1)
		}
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	}
	var sts *server.STSConfig
	if sc.STS != nil {
		sts = &server.STSConfig{Port: sc.STS.Port, Duration: sc.STS.Duration, Preload: sc.STS.Preload, Hostnames: sc.STS.Hostnames}
	}

	srv := server.New(server.Config{
		Name:         sc.Name,
		Network:      sc.Network,
		Listen:       sc.Listen,
		TLSListen:    sc.TLSListen,
		TLSConfig:    tlsConfig,
		MOTD:         sc.MOTD,
		MaxClients:   sc.MaxClients,
		Password:     sc.Password,
		PingInterval: sc.PingInterval,
		PingTimeout:  sc.PingTimeout,
		Opers:        opers,
		Accounts:     accounts,
		STS:          sts,
		Caps:         sc.Caps,
	})

	slog.Info("starting ircd", "version", version, "name", sc.Name, "network", sc.Network)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "ircd: %v\n", err)
		os.Exit(1)
	}
}

func runWithDefaults() {
	srv := server.New(server.Config{
		Name:    "irc.local",
		Network: "LocalNet",
		Listen:  ":6667",
		MOTD:    "Welcome to go-irc!",
	})
	slog.Info("starting ircd with defaults", "listen", ":6667")
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "ircd: %v\n", err)
		os.Exit(1)
	}
}
