// Package server implements an FTP server.
//
// The Server is the entry point. It accepts TCP connections, hands
// them off to a per-connection handler, and exposes hooks for
// authentication and virtual filesystem backends. The default
// configuration is a permissive anonymous server rooted at the
// current working directory.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/natalie-o-perret/go-ftp/server/auth"
	"github.com/natalie-o-perret/go-ftp/server/fs"
)

// Config is the server configuration.
type Config struct {
	// Name is the name advertised in the greeting and SYST replies.
	Name string
	// Listen is host:port. ":21" is the default.
	Listen string
	// Banner is sent on connect. The default is "Welcome to <Name>".
	Banner string
	// MaxConnections caps simultaneous sessions. 0 means unlimited.
	MaxConnections int
	// ReadTimeout is the per-read deadline on the control channel.
	ReadTimeout time.Duration
	// WriteTimeout is the per-write deadline on the control channel.
	WriteTimeout time.Duration
	// Authenticator returns the resolved user for a (user, pass)
	// pair. The default is anonymous-only.
	Authenticator auth.Authenticator
	// Filesystem is the virtual filesystem served to authenticated
	// users. The default is an OS-backed filesystem rooted at ".".
	Filesystem fs.Filesystem
	// TLS, if set, makes the server accept implicit FTPS on Listen.
	// The data channel is not TLS-protected by default.
	TLS *tls.Config
	// WelcomeMessage is sent to clients after login.
	WelcomeMessage string
	// PassivePortRange restricts the data-channel ports used for
	// PASV/EPSV. The default is the kernel-assigned range.
	PassivePortRange PortRange
	// Logger receives diagnostic lines. Optional.
	Logger Logger
}

// PortRange is a closed interval of TCP ports used for passive data
// channels.
type PortRange struct {
	Start int
	End   int
}

// Logger is the minimal logging interface the server uses.
// *log.Logger satisfies this interface.
type Logger interface {
	Printf(format string, args ...any)
}

// Server is an FTP server. A single Server can serve one listener.
// For multiple listeners, create multiple Servers.
type Server struct {
	cfg     Config
	ln      net.Listener
	sessMu  sync.Mutex
	sessCnt int
	wg      sync.WaitGroup
	closed  chan struct{}
}

// New constructs a Server. Defaults are applied for any zero-value
// fields.
func New(cfg Config) (*Server, error) {
	if cfg.Name == "" {
		cfg.Name = "go-ftp"
	}
	if cfg.Listen == "" {
		cfg.Listen = ":21"
	}
	if cfg.Banner == "" {
		cfg.Banner = "Welcome to " + cfg.Name
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 5 * time.Minute
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 30 * time.Second
	}
	if cfg.Authenticator == nil {
		cfg.Authenticator = auth.AllowAnonymous()
	}
	if cfg.Filesystem == nil {
		cfg.Filesystem = fs.NewOS(".")
	}
	return &Server{cfg: cfg, closed: make(chan struct{})}, nil
}

// ListenAndServe blocks accepting connections. It returns when the
// listener is closed (via Shutdown) or a fatal accept error occurs.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	if s.cfg.TLS != nil {
		ln = tls.NewListener(ln, s.cfg.TLS)
	}
	s.ln = ln
	return s.serveLoop(ln)
}

// Serve serves on the provided listener.
func (s *Server) Serve(ln net.Listener) error {
	s.ln = ln
	return s.serveLoop(ln)
}

// Addr returns the bound address. Useful when Listen was ":0".
func (s *Server) Addr() net.Addr {
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// Shutdown stops accepting new connections and waits for in-flight
// sessions to drain.
func (s *Server) Shutdown(ctx context.Context) error {
	close(s.closed)
	var err error
	if s.ln != nil {
		err = s.ln.Close()
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) serveLoop(ln net.Listener) error {
	defer func() { _ = ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			select {
			case <-s.closed:
				return nil
			default:
			}
			return err
		}
		if s.cfg.MaxConnections > 0 {
			s.sessMu.Lock()
			if s.sessCnt >= s.cfg.MaxConnections {
				s.sessMu.Unlock()
				_ = conn.Close()
				continue
			}
			s.sessCnt++
			s.sessMu.Unlock()
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				if s.cfg.MaxConnections > 0 {
					s.sessMu.Lock()
					s.sessCnt--
					s.sessMu.Unlock()
				}
			}()
			s.handleConn(conn)
		}()
	}
}

// handleConn drives a single client session. The session lives until
// the client disconnects or sends QUIT.
func (s *Server) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	remote := conn.RemoteAddr().String()
	sess := newSession(s, conn)
	if err := sess.greet(); err != nil {
		s.logf("greet %s: %v", remote, err)
		return
	}
	if err := sess.serve(); err != nil {
		s.logf("serve %s: %v", remote, err)
	}
}

func (s *Server) logf(format string, args ...any) {
	if s.cfg.Logger != nil {
		s.cfg.Logger.Printf(format, args...)
		return
	}
	fmt.Printf("["+s.cfg.Name+"] "+format+"\n", args...)
}
