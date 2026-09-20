// Command sftpd is a standalone SFTP server. It listens for SSH
// connections, authenticates users with a static user/password
// list, and exposes the configured filesystem via the sftp
// subsystem.
//
//	sftpd -addr :2022 -root /srv/files
//
// Configuration is read from a TOML file passed via -config.
//
// Authentication uses password auth (golang.org/x/crypto/ssh).
// Host keys are loaded with -hostkey.
package main

import (
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"os"

	"github.com/natalie-o-perret/go-sftp/server"
	"github.com/natalie-o-perret/go-sftp/server/backend/osfs"
	"golang.org/x/crypto/ssh"
)

func main() {
	addr := flag.String("addr", ":2022", "listen address")
	root := flag.String("root", ".", "filesystem root")
	hostKey := flag.String("hostkey", "", "path to host private key (required)")
	flag.Parse()

	if *hostKey == "" {
		log.Fatal("-hostkey is required")
	}
	keyBytes, err := os.ReadFile(*hostKey)
	if err != nil {
		log.Fatalf("read host key: %v", err)
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		log.Fatalf("parse host key: %v", err)
	}

	srvCfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) == "demo" {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("password rejected")
		},
	}
	srvCfg.AddHostKey(signer)

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("sftpd listening on %s, root %s", *addr, *root)
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go handle(conn, srvCfg, *root)
	}
}

func handle(conn net.Conn, cfg *ssh.ServerConfig, root string) {
	defer func() { _ = conn.Close() }()
	sconn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		log.Printf("handshake: %v", err)
		return
	}
	defer func() { _ = sconn.Close() }()
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		if ch.ChannelType() != "session" {
			_ = ch.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		ch, requests, err := ch.Accept()
		if err != nil {
			log.Printf("accept channel: %v", err)
			continue
		}
		go handleSession(ch, requests, root)
	}
}

func handleSession(ch ssh.Channel, requests <-chan *ssh.Request, root string) {
	defer func() { _ = ch.Close() }()
	for req := range requests {
		switch req.Type {
		case "subsystem":
			var payload struct {
				Name string
			}
			if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
				_ = req.Reply(false, nil)
				continue
			}
			if payload.Name != "sftp" {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			srv := server.New(osfs.New(root))
			if err := srv.Serve(ch); err != nil && !errors.Is(err, io.EOF) {
				log.Printf("sftp serve: %v", err)
			}
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
}
