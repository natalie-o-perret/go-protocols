package client

import (
	"bufio"
	"crypto/tls"
	"strconv"
	"strings"

	"github.com/natalie-o-perret/go-ftp/ftp"
)

// Login authenticates with USER/PASS. If the server accepts a
// response-less USER it skips the PASS step. An account argument can
// be passed for ACCT-supporting servers.
func (c *Client) Login(user, pass, account string) error {
	if user == "" {
		user = "anonymous"
	}
	if pass == "" {
		pass = "anonymous@"
	}
	reply, err := c.Do(ftp.CmdUSER, user)
	if err != nil {
		return err
	}
	if reply.Code == ftp.CodeLoginOK {
		return nil
	}
	if reply.Code != ftp.CodeUserOK && reply.Code != ftp.CodeNeedPassword {
		return reply
	}
	reply, err = c.Do(ftp.CmdPASS, pass)
	if err != nil {
		return err
	}
	if reply.Code == ftp.CodeLoginOK {
		return nil
	}
	if reply.Code == ftp.CodeNeedPassword && account != "" {
		reply, err = c.Do(ftp.CmdACCT, account)
		if err != nil {
			return err
		}
	}
	if !reply.Positive() {
		return reply
	}
	return nil
}

// Feat requests the server's feature list and returns it.
func (c *Client) Feat() ([]string, error) {
	reply, err := c.Do(ftp.CmdFEAT, "")
	if err != nil {
		return nil, err
	}
	if reply.Code == ftp.CodeCommandNotImpl {
		return nil, nil
	}
	c.features = reply.Lines
	return reply.Lines, nil
}

// Supports reports whether the server advertised the given feature
// during a previous Feat call. The match is case-insensitive and
// matches whole words, so Supports(c, "UTF8") matches "UTF8" and
// "utf8" but not "UTF8X".
func (c *Client) Supports(feature string) bool {
	up := strings.ToUpper(feature)
	for _, f := range c.features {
		if strings.ToUpper(strings.TrimSpace(f)) == up {
			return true
		}
	}
	return false
}

// Pwd returns the current directory as reported by PWD. The returned
// string is the textual directory name (e.g. "/pub") and does not
// include the surrounding quotes RFC 959 mandates.
func (c *Client) Pwd() (string, error) {
	reply, err := c.Do(ftp.CmdPWD, "")
	if err != nil {
		return "", err
	}
	if reply.Code != ftp.CodePathCreated {
		return "", reply
	}
	return unwrapQuoted(reply.Text), nil
}

// Cwd changes the working directory.
func (c *Client) Cwd(path string) error {
	reply, err := c.Do(ftp.CmdCWD, path)
	if err != nil {
		return err
	}
	if !reply.Positive() {
		return reply
	}
	return nil
}

// Cdup is shorthand for Cwd("..").
func (c *Client) Cdup() error {
	return c.Cwd("..")
}

// Mkd creates a directory. The full path returned by the server is
// returned to the caller.
func (c *Client) Mkd(path string) (string, error) {
	reply, err := c.Do(ftp.CmdMKD, path)
	if err != nil {
		return "", err
	}
	if reply.Code != ftp.CodePathCreated {
		return "", reply
	}
	return unwrapQuoted(reply.Text), nil
}

// Rmd removes a directory.
func (c *Client) Rmd(path string) error {
	reply, err := c.Do(ftp.CmdRMD, path)
	if err != nil {
		return err
	}
	if !reply.Positive() {
		return reply
	}
	return nil
}

// Dele removes a file.
func (c *Client) Dele(path string) error {
	reply, err := c.Do(ftp.CmdDELE, path)
	if err != nil {
		return err
	}
	if !reply.Positive() {
		return reply
	}
	return nil
}

// Rename renames from to to.
func (c *Client) Rename(from, to string) error {
	if reply, err := c.Do(ftp.CmdRNFR, from); err != nil {
		return err
	} else if reply.Code != ftp.CodeRequestedAction && reply.Code != 350 {
		return reply
	}
	reply, err := c.Do(ftp.CmdRNTO, to)
	if err != nil {
		return err
	}
	if !reply.Positive() {
		return reply
	}
	return nil
}

// Size returns the size of path in bytes. Some servers do not support
// SIZE for directories; callers should treat a 550 as "not a regular
// file" rather than a fatal error.
func (c *Client) Size(path string) (int64, error) {
	reply, err := c.Do(ftp.CmdSIZE, path)
	if err != nil {
		return 0, err
	}
	if reply.Code != ftp.CodeFileStatus {
		return 0, reply
	}
	return strconv.ParseInt(strings.TrimSpace(reply.Text), 10, 64)
}

// Mdtm returns the modification time of path as the server reports it
// (RFC 3659). The format is "YYYYMMDDhhmmss" in UTC.
func (c *Client) Mdtm(path string) (string, error) {
	reply, err := c.Do(ftp.CmdMDTM, path)
	if err != nil {
		return "", err
	}
	if reply.Code != ftp.CodeFileStatus {
		return "", reply
	}
	return strings.TrimSpace(reply.Text), nil
}

// Type issues the TYPE command. repr is the type (typically "A" for
// ASCII or "I" for image/binary) and the optional second argument
// sets the format control for ASCII transfers.
func (c *Client) Type(repr string, formatControl ...string) error {
	arg := repr
	for _, fc := range formatControl {
		arg += " " + fc
	}
	reply, err := c.Do(ftp.CmdTYPE, arg)
	if err != nil {
		return err
	}
	if !reply.Positive() {
		return reply
	}
	return nil
}

// Pasv asks the server to listen on a data port and returns the
// resulting host:port pair.
func (c *Client) Pasv() (host string, port int, err error) {
	reply, err := c.Do(ftp.CmdPASV, "")
	if err != nil {
		return "", 0, err
	}
	return ftp.ParsePASVReply(reply)
}

// Epsv asks the server to listen on a data port and returns just the
// port. The caller dials the same host as the control connection.
func (c *Client) Epsv() (port int, err error) {
	if c.cfg.DisableEPSV {
		_, p, e := c.Pasv()
		if e != nil {
			return 0, e
		}
		return p, nil
	}
	reply, err := c.Do(ftp.CmdEPSV, "")
	if err != nil {
		return 0, err
	}
	if reply.Code == ftp.CodeCommandNotImpl || reply.Code == 500 {
		_, p, e := c.Pasv()
		if e != nil {
			return 0, e
		}
		return p, nil
	}
	portStr, err := ftp.ParseEPSVReply(reply)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(portStr)
}

// Port asks the server to connect to the given address for the next
// data transfer. host/port is the address the server should dial.
func (c *Client) Port(host string, port int) error {
	reply, err := c.Do(ftp.CmdPORT, ftp.FormatPORT(host, port))
	if err != nil {
		return err
	}
	if !reply.Positive() {
		return reply
	}
	return nil
}

// Eprt asks the server to connect to host:port for the next data
// transfer using the named protocol (1 = IPv4, 2 = IPv6).
func (c *Client) Eprt(proto, host string, port int) error {
	reply, err := c.Do(ftp.CmdEPRT, ftp.FormatEPRT(proto, host, port))
	if err != nil {
		return err
	}
	if !reply.Positive() {
		return reply
	}
	return nil
}

// Rest sets the restart offset for the next RETR.
func (c *Client) Rest(offset int64) error {
	reply, err := c.Do(ftp.CmdREST, strconv.FormatInt(offset, 10))
	if err != nil {
		return err
	}
	if !reply.Positive() {
		return reply
	}
	return nil
}

// Noop sends NOOP and returns the server's reply. Useful to keep an
// idle connection alive.
func (c *Client) Noop() error {
	reply, err := c.Do(ftp.CmdNOOP, "")
	if err != nil {
		return err
	}
	if !reply.Positive() {
		return reply
	}
	return nil
}

// AuthTLS upgrades the control channel to TLS. The data channel is
// not affected; see Prot.
func (c *Client) AuthTLS(mech string) error {
	if mech == "" {
		mech = "TLS"
	}
	reply, err := c.Do(ftp.CmdAUTH, mech)
	if err != nil {
		return err
	}
	if reply.Code != ftp.CodeServiceReadyTLS {
		return reply
	}
	cfg := c.cfg.TLSConfig
	if cfg == nil {
		cfg = &tls.Config{ServerName: c.host}
	}
	tlsConn := tls.Client(c.ctrl, cfg)
	if err := tlsConn.Handshake(); err != nil {
		return err
	}
	c.ctrl = tlsConn
	c.r = ftp.NewReplyScanner(tlsConn)
	c.w = bufio.NewWriter(tlsConn)
	return nil
}

// Prot sets the data channel protection level. "P" means private
// (TLS), "C" means clear.
func (c *Client) Prot(level string) error {
	reply, err := c.Do(ftp.CmdPROT, level)
	if err != nil {
		return err
	}
	if !reply.Positive() {
		return reply
	}
	return nil
}

// PBSZ sets the protection buffer size. The protocol mandates a
// single fixed value of 0; this is a no-op to the protocol but
// required by RFC 4217.
func (c *Client) PBSZ(size int) error {
	reply, err := c.Do(ftp.CmdPBSZ, strconv.Itoa(size))
	if err != nil {
		return err
	}
	if !reply.Positive() {
		return reply
	}
	return nil
}

// Opts negotiates an option value with the server. The argument is
// the raw "feature value" string.
func (c *Client) Opts(feature, value string) error {
	reply, err := c.Do(ftp.CmdOPTS, feature+" "+value)
	if err != nil {
		return err
	}
	if !reply.Positive() {
		return reply
	}
	return nil
}

func unwrapQuoted(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && len(s) >= 2 {
		return s[1 : len(s)-1]
	}
	return s
}
