// Command ftpd is a standalone FTP server.
//
// Configuration is read from a TOML file passed via -config. If no
// file is given, the server listens on :21 with anonymous access
// rooted at the current working directory.
//
//	ftpd -config ftpd.toml
//
// See config/ftpd.example.toml for a full example.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/natalie-o-perret/go-ftp/config"
	"github.com/natalie-o-perret/go-ftp/server"
	"github.com/natalie-o-perret/go-ftp/server/auth"
	"github.com/natalie-o-perret/go-ftp/server/fs"
)

func main() {
	cfgPath := flag.String("config", "", "path to TOML config file")
	addr := flag.String("addr", "", "listen address (overrides config)")
	root := flag.String("root", "", "filesystem root (overrides config)")
	flag.Parse()

	cfg := config.Default()
	if *cfgPath != "" {
		f, err := os.Open(*cfgPath)
		if err != nil {
			log.Fatalf("open config: %v", err)
		}
		if err := config.Load(f, cfg); err != nil {
			log.Fatalf("load config: %v", err)
		}
		_ = f.Close()
	}
	if *addr != "" {
		cfg.Server.Listen = *addr
	}
	if *root != "" {
		cfg.Root = *root
	}

	srvCfg := server.Config{
		Name:           cfg.Server.Name,
		Listen:         cfg.Server.Listen,
		Banner:         cfg.Server.Banner,
		WelcomeMessage: cfg.Server.WelcomeMessage,
		Authenticator:  buildAuth(cfg),
		Filesystem:     fs.NewOS(cfg.Root),
		Logger:         log.New(os.Stderr, "ftpd ", log.LstdFlags),
	}
	if cfg.Server.PassivePortStart > 0 {
		srvCfg.PassivePortRange = server.PortRange{
			Start: cfg.Server.PassivePortStart,
			End:   cfg.Server.PassivePortEnd,
		}
	}
	srv, err := server.New(srvCfg)
	if err != nil {
		log.Fatalf("init server: %v", err)
	}
	fmt.Printf("ftpd listening on %s, root %s\n", srvCfg.Listen, cfg.Root)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func buildAuth(cfg *config.File) auth.Authenticator {
	if len(cfg.Users) == 0 {
		return auth.AllowAnonymous()
	}
	st := auth.NewStatic()
	for _, u := range cfg.Users {
		st.Add(u.Name, u.Password)
	}
	return st
}
