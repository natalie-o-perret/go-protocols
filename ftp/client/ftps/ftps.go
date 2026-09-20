// Package ftps adds explicit and implicit FTPS support on top of the
// plain FTP client. The package is a thin layer that performs the
// AUTH TLS upgrade (explicit) or the TLS-on-dial (implicit) and
// manages the data-channel protection level.
package ftps

import (
	"crypto/tls"
	"fmt"

	"github.com/natalie-o-perret/go-ftp/client"
	"github.com/natalie-o-perret/go-ftp/ftp"
)

// Config extends client.Config with FTPS-specific options.
type Config struct {
	client.Config
	// Explicit asks for an AUTH TLS upgrade on a plain TCP dial.
	// If false, the connection is dialed with TLS from the start
	// (implicit FTPS, port 990).
	Explicit bool
	// ProtectDataChannel enables PROT P after the upgrade so that
	// the data channel is also encrypted.
	ProtectDataChannel bool
}

// Connect dials an FTPS server and returns an authenticated client.
// If Config.User is set, the server is logged in before returning.
// If Config.Explicit is true, AUTH TLS is sent; otherwise an
// implicit TLS dial is performed.
func Connect(cfg Config) (*client.Client, error) {
	if cfg.TLSConfig == nil {
		return nil, fmt.Errorf("ftps: nil TLSConfig")
	}
	var c *client.Client
	if cfg.Explicit {
		cl, err := client.New(cfg.Config)
		if err != nil {
			return nil, err
		}
		c = cl
		if err := c.AuthTLS("TLS"); err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("ftps: AUTH TLS: %w", err)
		}
		if err := c.PBSZ(0); err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("ftps: PBSZ: %w", err)
		}
		if cfg.ProtectDataChannel {
			if err := c.Prot("P"); err != nil {
				_ = c.Close()
				return nil, fmt.Errorf("ftps: PROT P: %w", err)
			}
		} else {
			if err := c.Prot("C"); err != nil {
				_ = c.Close()
				return nil, fmt.Errorf("ftps: PROT C: %w", err)
			}
		}
	} else {
		cl, err := client.DialTLS(cfg.Config)
		if err != nil {
			return nil, err
		}
		c = cl
		if err := c.PBSZ(0); err != nil {
			_ = c.Close()
			return nil, err
		}
		if cfg.ProtectDataChannel {
			if err := c.Prot("P"); err != nil {
				_ = c.Close()
				return nil, err
			}
		} else {
			if err := c.Prot("C"); err != nil {
				_ = c.Close()
				return nil, err
			}
		}
	}
	if cfg.User != "" {
		if err := c.Login(cfg.User, cfg.Password, ""); err != nil {
			_ = c.Close()
			return nil, err
		}
	}
	return c, nil
}

// Wrap returns a client that has already been TLS-upgraded; useful
// for tests or for users who have already performed the AUTH
// handshake themselves.
func Wrap(c *client.Client, protect bool) error {
	if err := c.PBSZ(0); err != nil {
		return err
	}
	if protect {
		return c.Prot("P")
	}
	return c.Prot("C")
}

// ErrorIsReply reports whether err is a non-nil ftp.Reply error.
// This is the typical way to inspect a returned error from a typed
// helper to see if the server rejected the request.
func ErrorIsReply(err error) (ftp.Reply, bool) {
	if err == nil {
		return ftp.Reply{}, false
	}
	if r, ok := err.(ftp.Reply); ok {
		return r, true
	}
	return ftp.Reply{}, false
}

// DefaultTLSConfig returns a *tls.Config that requires at least TLS
// 1.2. Callers are expected to set ServerName and may set
// InsecureSkipVerify for testing.
func DefaultTLSConfig(serverName string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
	}
}
