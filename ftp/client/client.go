// Package client implements an FTP control-channel client. It manages
// the connection lifecycle, sends commands, parses replies, and
// exposes typed helpers for common operations (LIST, RETR, STOR).
//
// The data channel is opened by issuing PASV/EPSV and the client
// returns the listening address. The caller is responsible for dialing
// the data channel and performing the actual transfer; see the
// datatypes sub-package for ready-made Reader/Writer wrappers.
package client

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/natalie-o-perret/go-ftp/ftp"
)

// Config holds connection parameters. Zero values are replaced with
// sensible defaults: port 21, dial timeout 30s, control read timeout
// 2 minutes.
type Config struct {
	// Addr is host:port. If port is omitted, 21 is used.
	Addr string
	// User defaults to "anonymous".
	User string
	// Password defaults to "anonymous@".
	Password string
	// DialTimeout is the TCP dial timeout. 0 means 30s.
	DialTimeout time.Duration
	// ReadTimeout is the per-read deadline on the control channel. 0
	// means 2 minutes. A transfer in progress is expected to use a
	// data-channel Read/Write and is not affected.
	ReadTimeout time.Duration
	// TLSConfig is the configuration to use for the implicit FTPS
	// dial or the explicit FTPS upgrade. nil disables TLS.
	TLSConfig *tls.Config
	// DisableEPSV forces the use of PASV rather than EPSV. Some
	// servers reply incorrectly to EPSV.
	DisableEPSV bool
	// Logger receives diagnostic lines. Optional.
	Logger Logger
}

// Logger is the minimal logging interface the client uses. *log.Logger
// satisfies this interface.
type Logger interface {
	Printf(format string, args ...any)
}

// Client is a single FTP control channel connection. It is not safe to
// share a Client across goroutines; create one per connection.
type Client struct {
	cfg      Config
	ctrl     net.Conn
	r        *ftp.ReplyScanner
	w        *bufio.Writer
	mu       sync.Mutex
	host     string
	port     string
	features []string
}

// New dials the control channel and returns a Client in the
// unauthenticated state. Call Login to complete authentication.
func New(cfg Config) (*Client, error) {
	if cfg.Addr == "" {
		return nil, fmt.Errorf("ftp: empty Addr")
	}
	host, port, err := splitHostPort(cfg.Addr)
	if err != nil {
		return nil, err
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 30 * time.Second
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 2 * time.Minute
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), cfg.DialTimeout)
	if err != nil {
		return nil, err
	}
	c := &Client{cfg: cfg, ctrl: conn, host: host, port: port,
		r: ftp.NewReplyScanner(conn), w: bufio.NewWriter(conn)}
	greet, err := c.readReply()
	if err != nil {
		_ = c.ctrl.Close()
		return nil, err
	}
	if !greet.Positive() {
		_ = c.ctrl.Close()
		return nil, greet
	}
	return c, nil
}

// DialTLS performs an implicit FTPS connect: TLS is negotiated
// immediately on the underlying TCP connection.
func DialTLS(cfg Config) (*Client, error) {
	if cfg.TLSConfig == nil {
		return nil, fmt.Errorf("ftp: DialTLS requires a non-nil TLSConfig")
	}
	host, port, err := splitHostPort(cfg.Addr)
	if err != nil {
		return nil, err
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 30 * time.Second
	}
	raw, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), cfg.DialTimeout)
	if err != nil {
		return nil, err
	}
	tlsConn := tls.Client(raw, cfg.TLSConfig)
	if err := tlsConn.Handshake(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	c := &Client{cfg: cfg, ctrl: tlsConn, host: host, port: port,
		r: ftp.NewReplyScanner(tlsConn), w: bufio.NewWriter(tlsConn)}
	greet, err := c.readReply()
	if err != nil {
		_ = c.ctrl.Close()
		return nil, err
	}
	if !greet.Positive() {
		_ = c.ctrl.Close()
		return nil, greet
	}
	return c, nil
}

// Close closes the control channel.
func (c *Client) Close() error {
	if c.ctrl == nil {
		return nil
	}
	_ = c.SendCommand(ftp.CmdQUIT, "")
	err := c.ctrl.Close()
	c.ctrl = nil
	return err
}

// Addr returns the remote address of the control channel.
func (c *Client) Addr() string { return c.ctrl.RemoteAddr().String() }

// SendCommand writes a single command and flushes.
func (c *Client) SendCommand(name, arg string) error {
	cmd := ftp.Command{Name: name, Arg: arg}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.w.WriteString(cmd.String()); err != nil {
		return err
	}
	return c.w.Flush()
}

// ReadReply reads the next single reply from the control channel.
// Multi-line replies are returned as a single Reply with Lines set.
func (c *Client) ReadReply() (ftp.Reply, error) {
	return c.readReply()
}

func (c *Client) readReply() (ftp.Reply, error) {
	if c.cfg.ReadTimeout > 0 {
		_ = c.ctrl.SetReadDeadline(time.Now().Add(c.cfg.ReadTimeout))
	}
	return c.r.Read()
}

// Do sends a command and reads a single reply. It does not enforce
// any particular completion code; callers that need a specific
// response should use the typed helpers.
func (c *Client) Do(name, arg string) (ftp.Reply, error) {
	if err := c.SendCommand(name, arg); err != nil {
		return ftp.Reply{}, err
	}
	return c.readReply()
}

func splitHostPort(addr string) (string, string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", "", err
	}
	if port == "" {
		port = "21"
	}
	return host, port, nil
}
