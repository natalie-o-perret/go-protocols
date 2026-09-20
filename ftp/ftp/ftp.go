// Package ftp implements the File Transfer Protocol as defined by RFC 959,
// with the common extensions from RFC 2228 (AUTH/SSL), RFC 2389 (FEAT),
// RFC 2428 (EPSV/EPRT) and RFC 2640 (UTF-8).
//
// It exposes the protocol primitives used by both the client and server
// sub-packages: the wire-level command and reply types, reply codes, and
// helpers for formatting and parsing the FTP control channel.
//
// The data channel is not handled here. See the client and server packages
// for higher-level connection management.
package ftp

import (
	"fmt"
	"strconv"
	"strings"
)

// Code is a 3-digit FTP reply code. The high digit classifies the kind of
// reply: 1xx Preliminary, 2xx Completion, 3xx Intermediate, 4xx Transient
// negative, 5xx Permanent negative.
type Code int

const (
	CodeReady           Code = 220
	CodeGoodbye         Code = 221
	CodeServiceReadyTLS Code = 234
	CodeUserOK          Code = 331
	CodeNotLoggedIn     Code = 332
	CodeLoginOK         Code = 230
	CodeRequestedAction Code = 250
	CodePathCreated     Code = 257
	CodeNeedPassword    Code = 332
	CodeOK              Code = 200
	CodeCommandNotImpl  Code = 502
	CodeNotImplemented  Code = 214
	CodeFileStatus      Code = 213
	CodeDataOpen        Code = 150
	CodeDataClose       Code = 226
	CodeEnterPASV       Code = 227
	CodeEnterEPSV       Code = 229
	CodeEnterExtPort    Code = 229
	CodeCantOpenData    Code = 425
	CodeConnClosed      Code = 426
	CodeFileActionOK    Code = 250
	CodeSyntaxError     Code = 500
	CodeNotLoggedInReq  Code = 530
	CodeFileUnavailable Code = 550
	CodeStorageExceeded Code = 552
	CodeBadFilename     Code = 553
)

// Reply is a single FTP control channel reply. Replies are textual lines
// that begin with a 3-digit code. Multi-line replies are encoded by the
// server as several consecutive lines and reassembled by the client.
type Reply struct {
	Code  Code
	Text  string
	Lines []string // populated for multi-line replies (211, 214, 215, ...)
}

// String returns the wire form of the reply. A single-line reply is
// "<code> <text>\r\n". A multi-line reply is "<code>-<text>\r\n"
// followed by a final "<code> <text>\r\n".
func (r Reply) String() string {
	if len(r.Lines) == 0 {
		return fmt.Sprintf("%d %s\r\n", r.Code, r.Text)
	}
	var b strings.Builder
	for i, l := range r.Lines {
		if i == len(r.Lines)-1 {
			fmt.Fprintf(&b, "%d %s\r\n", r.Code, l)
		} else {
			fmt.Fprintf(&b, "%d-%s\r\n", r.Code, l)
		}
	}
	return b.String()
}

// Error implements the error interface so a Reply with a 4xx or 5xx code
// can be returned as an error.
func (r Reply) Error() string {
	return fmt.Sprintf("ftp: %d %s", r.Code, r.Text)
}

// Permanent reports whether the reply is a 5xx permanent negative
// completion. A 4xx reply is a transient negative completion and may be
// retried.
func (r Reply) Permanent() bool {
	return r.Code >= 500 && r.Code < 600
}

// Positive reports whether the reply is a 2xx or 3xx completion.
func (r Reply) Positive() bool {
	return r.Code >= 200 && r.Code < 400
}

// Command is a parsed FTP control channel command.
type Command struct {
	Name string
	Arg  string
}

// String returns the wire form of the command. The argument is appended
// verbatim after a single space; if the argument contains spaces or
// control characters the caller is expected to have escaped them.
func (c Command) String() string {
	if c.Arg == "" {
		return c.Name + "\r\n"
	}
	return c.Name + " " + c.Arg + "\r\n"
}

// IsPrefix reports whether c begins with one of the given prefixes
// case-insensitively. FTP command verbs are case-insensitive.
func (c Command) IsPrefix(prefixes ...string) bool {
	up := strings.ToUpper(c.Name)
	for _, p := range prefixes {
		if up == p {
			return true
		}
	}
	return false
}

// Parse decodes a single FTP control channel line into a Command.
// The CRLF terminator must already be stripped.
func Parse(line string) (Command, error) {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return Command{}, fmt.Errorf("ftp: empty command")
	}
	parts := strings.SplitN(line, " ", 2)
	cmd := Command{Name: strings.ToUpper(parts[0])}
	if len(parts) == 2 {
		cmd.Arg = parts[1]
	}
	return cmd, nil
}

// FormatCode formats a Code as a three-digit string.
func FormatCode(c Code) string {
	return strconv.Itoa(int(c))
}

// NewReply constructs a single-line reply.
func NewReply(code Code, text string) Reply {
	return Reply{Code: code, Text: text}
}

// NewMultiLine constructs a multi-line reply. The last line of lines
// becomes the terminating line; the others are sent with the hyphen
// separator.
func NewMultiLine(code Code, lines ...string) Reply {
	return Reply{Code: code, Lines: lines}
}
