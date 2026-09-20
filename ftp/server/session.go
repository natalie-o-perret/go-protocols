package server

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/natalie-o-perret/go-ftp/ftp"
	"github.com/natalie-o-perret/go-ftp/server/auth"
	"github.com/natalie-o-perret/go-ftp/server/fs"
)

// session is one FTP control connection and its state.
type session struct {
	srv      *Server
	conn     net.Conn
	br       *bufio.Reader
	bw       *bufio.Writer
	wmu      sync.Mutex
	user     string
	auth     auth.User
	logged   bool
	cwd      string
	renameTo string
	rest     int64
	type_    string
	passive  *passiveListener
	dataTLS  bool
}

func newSession(s *Server, c net.Conn) *session {
	return &session{
		srv:   s,
		conn:  c,
		br:    bufio.NewReader(c),
		bw:    bufio.NewWriter(c),
		cwd:   "/",
		type_: "A",
	}
}

func (s *session) greet() error {
	return s.reply(ftp.NewReply(ftp.CodeReady, s.srv.cfg.Banner))
}

func (s *session) reply(r ftp.Reply) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(s.srv.cfg.WriteTimeout))
	_, err := s.bw.WriteString(r.String())
	if err != nil {
		return err
	}
	return s.bw.Flush()
}

func (s *session) serve() error {
	for {
		_ = s.conn.SetReadDeadline(time.Now().Add(s.srv.cfg.ReadTimeout))
		line, err := s.br.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		cmd, err := ftp.Parse(line)
		if err != nil {
			if rerr := s.reply(ftp.NewReply(ftp.CodeSyntaxError, "Syntax error")); rerr != nil {
				return rerr
			}
			continue
		}
		if err := s.dispatch(cmd); err != nil {
			if errors.Is(err, errQuit) {
				return nil
			}
			if rerr := s.reply(ftp.NewReply(ftp.CodeNotLoggedInReq, err.Error())); rerr != nil {
				return rerr
			}
		}
	}
}

var errQuit = errors.New("quit")

func (s *session) dispatch(c ftp.Command) error {
	if !s.loggedIn() && c.Name != ftp.CmdUSER && c.Name != ftp.CmdPASS &&
		c.Name != ftp.CmdQUIT && c.Name != ftp.CmdAUTH && c.Name != ftp.CmdFEAT &&
		c.Name != ftp.CmdNOOP && c.Name != ftp.CmdHELP && c.Name != ftp.CmdSYST &&
		c.Name != ftp.CmdPBSZ && c.Name != ftp.CmdPROT && c.Name != ftp.CmdCCC {
		return fmt.Errorf("please log in")
	}
	switch c.Name {
	case ftp.CmdUSER:
		return s.handleUSER(c.Arg)
	case ftp.CmdPASS:
		return s.handlePASS(c.Arg)
	case ftp.CmdQUIT:
		_ = s.reply(ftp.NewReply(ftp.CodeGoodbye, "Goodbye"))
		return errQuit
	case ftp.CmdNOOP:
		return s.reply(ftp.NewReply(ftp.CodeOK, "NOOP ok"))
	case ftp.CmdSYST:
		return s.reply(ftp.NewReply(215, "UNIX Type: L8"))
	case ftp.CmdFEAT:
		return s.reply(ftp.NewMultiLine(211, "End"))
	case ftp.CmdHELP:
		return s.reply(ftp.NewReply(214, "Help me help you"))
	case ftp.CmdPWD:
		return s.reply(ftp.NewReply(ftp.CodePathCreated, fmt.Sprintf("%q", s.cwd)))
	case ftp.CmdCWD:
		return s.cwd2(c.Arg)
	case ftp.CmdCDUP:
		return s.cwd2("..")
	case ftp.CmdTYPE:
		return s.type2(c.Arg)
	case ftp.CmdPASV:
		return s.pasv()
	case ftp.CmdEPSV:
		return s.epsv()
	case ftp.CmdPORT:
		return s.port(c.Arg)
	case ftp.CmdEPRT:
		return s.eprt(c.Arg)
	case ftp.CmdLIST:
		return s.list(c.Arg)
	case ftp.CmdNLST:
		return s.list(c.Arg)
	case ftp.CmdRETR:
		return s.retr(c.Arg)
	case ftp.CmdSTOR:
		return s.stor(c.Arg)
	case ftp.CmdAPPE:
		return s.appe(c.Arg)
	case ftp.CmdDELE:
		return s.dele(c.Arg)
	case ftp.CmdRMD:
		return s.rmd(c.Arg)
	case ftp.CmdMKD:
		return s.mkd(c.Arg)
	case ftp.CmdRNFR:
		return s.rnfr(c.Arg)
	case ftp.CmdRNTO:
		return s.rnto(c.Arg)
	case ftp.CmdREST:
		return s.rest2(c.Arg)
	case ftp.CmdSIZE:
		return s.size(c.Arg)
	case ftp.CmdMDTM:
		return s.mdtm(c.Arg)
	case ftp.CmdAUTH:
		return s.auth2(c.Arg)
	case ftp.CmdPBSZ:
		return s.reply(ftp.NewReply(200, "PBSZ set to 0"))
	case ftp.CmdPROT:
		return s.prot(c.Arg)
	}
	return s.reply(ftp.NewReply(ftp.CodeCommandNotImpl, "Not implemented"))
}

func (s *session) loggedIn() bool { return s.logged }

func (s *session) handleUSER(arg string) error {
	s.user = arg
	s.auth = auth.User{}
	s.logged = false
	return s.reply(ftp.NewReply(ftp.CodeUserOK, "Password required"))
}

func (s *session) handlePASS(arg string) error {
	if s.user == "" {
		return s.reply(ftp.NewReply(ftp.CodeNotLoggedInReq, "Login with USER first"))
	}
	u, err := s.srv.cfg.Authenticator.Authenticate(s.user, arg)
	if err != nil {
		return s.reply(ftp.NewReply(530, err.Error()))
	}
	s.auth = u
	s.logged = true
	s.cwd = "/"
	msg := s.srv.cfg.WelcomeMessage
	if msg == "" {
		msg = "Login successful"
	}
	return s.reply(ftp.NewReply(ftp.CodeLoginOK, msg))
}

func (s *session) cwd2(arg string) error {
	target, err := s.srv.cfg.Filesystem.Resolve(s.auth, s.cwd, arg)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	stat, err := s.srv.cfg.Filesystem.Stat(s.auth, target)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	if !stat.IsDir {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, "Not a directory"))
	}
	s.cwd = target
	return s.reply(ftp.NewReply(ftp.CodeRequestedAction, "CWD successful"))
}

func (s *session) type2(arg string) error {
	arg = strings.ToUpper(arg)
	if arg != "A" && arg != "I" {
		return s.reply(ftp.NewReply(ftp.CodeSyntaxError, "Unsupported type"))
	}
	s.type_ = arg
	return s.reply(ftp.NewReply(200, "Type set to "+arg))
}

func (s *session) pasv() error {
	ln, err := s.openPassive()
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeCantOpenData, err.Error()))
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	reply := ftp.NewReply(ftp.CodeEnterPASV, fmt.Sprintf("Entering Passive Mode (%s).",
		strings.ReplaceAll(host, ".", ",")+","+strconv.Itoa(mustAtoi(port)/256)+","+strconv.Itoa(mustAtoi(port)%256)))
	s.passive = &passiveListener{ln: ln}
	return s.reply(reply)
}

func (s *session) epsv() error {
	ln, err := s.openPassive()
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeCantOpenData, err.Error()))
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	s.passive = &passiveListener{ln: ln}
	return s.reply(ftp.NewReply(ftp.CodeEnterEPSV,
		fmt.Sprintf("Entering Extended Passive Mode (|||%s|).", port)))
}

func (s *session) port(arg string) error {
	host, port, err := parsePORT(arg)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeSyntaxError, err.Error()))
	}
	s.passive = &passiveListener{remote: net.JoinHostPort(host, strconv.Itoa(port))}
	return s.reply(ftp.NewReply(200, "PORT command successful"))
}

func (s *session) eprt(arg string) error {
	parts := strings.Split(arg, "|")
	if len(parts) != 4 {
		return s.reply(ftp.NewReply(ftp.CodeSyntaxError, "Bad EPRT"))
	}
	port, err := strconv.Atoi(parts[2])
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeSyntaxError, "Bad EPRT port"))
	}
	s.passive = &passiveListener{remote: net.JoinHostPort(parts[1], strconv.Itoa(port))}
	return s.reply(ftp.NewReply(200, "EPRT command successful"))
}

func (s *session) openPassive() (net.Listener, error) {
	if s.srv.cfg.PassivePortRange.End > 0 {
		for port := s.srv.cfg.PassivePortRange.Start; port <= s.srv.cfg.PassivePortRange.End; port++ {
			ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
			if err == nil {
				return ln, nil
			}
		}
		return nil, fmt.Errorf("no passive port available")
	}
	return net.Listen("tcp", "0.0.0.0:0")
}

func (s *session) list(arg string) error {
	target := s.cwd
	if arg != "" {
		var err error
		target, err = s.srv.cfg.Filesystem.Resolve(s.auth, s.cwd, arg)
		if err != nil {
			return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
		}
	}
	entries, err := s.srv.cfg.Filesystem.List(s.auth, target)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	conn, err := s.acceptData()
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeCantOpenData, err.Error()))
	}
	defer func() { _ = conn.Close() }()
	if err := s.reply(ftp.NewReply(ftp.CodeDataOpen, "Opening data connection")); err != nil {
		return err
	}
	for _, e := range entries {
		_, _ = io.WriteString(conn, formatListEntry(e)+"\r\n")
	}
	return s.reply(ftp.NewReply(ftp.CodeDataClose, "Transfer complete"))
}

func (s *session) retr(arg string) error {
	target, err := s.srv.cfg.Filesystem.Resolve(s.auth, s.cwd, arg)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	conn, err := s.acceptData()
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeCantOpenData, err.Error()))
	}
	defer func() { _ = conn.Close() }()
	if err := s.reply(ftp.NewReply(ftp.CodeDataOpen, "Opening data connection")); err != nil {
		return err
	}
	if err := s.srv.cfg.Filesystem.SendFile(s.auth, target, conn, s.rest); err != nil {
		_ = s.reply(ftp.NewReply(ftp.CodeConnClosed, err.Error()))
		return nil
	}
	return s.reply(ftp.NewReply(ftp.CodeDataClose, "Transfer complete"))
}

func (s *session) stor(arg string) error {
	target, err := s.srv.cfg.Filesystem.Resolve(s.auth, s.cwd, arg)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	conn, err := s.acceptData()
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeCantOpenData, err.Error()))
	}
	defer func() { _ = conn.Close() }()
	if err := s.reply(ftp.NewReply(ftp.CodeDataOpen, "Opening data connection")); err != nil {
		return err
	}
	if err := s.srv.cfg.Filesystem.RecvFile(s.auth, target, conn); err != nil {
		_ = s.reply(ftp.NewReply(ftp.CodeStorageExceeded, err.Error()))
		return nil
	}
	return s.reply(ftp.NewReply(ftp.CodeDataClose, "Transfer complete"))
}

func (s *session) appe(arg string) error {
	target, err := s.srv.cfg.Filesystem.Resolve(s.auth, s.cwd, arg)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	conn, err := s.acceptData()
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeCantOpenData, err.Error()))
	}
	defer func() { _ = conn.Close() }()
	if err := s.reply(ftp.NewReply(ftp.CodeDataOpen, "Opening data connection")); err != nil {
		return err
	}
	if err := s.srv.cfg.Filesystem.AppendFile(s.auth, target, conn); err != nil {
		_ = s.reply(ftp.NewReply(ftp.CodeStorageExceeded, err.Error()))
		return nil
	}
	return s.reply(ftp.NewReply(ftp.CodeDataClose, "Transfer complete"))
}

func (s *session) dele(arg string) error {
	target, err := s.srv.cfg.Filesystem.Resolve(s.auth, s.cwd, arg)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	if err := s.srv.cfg.Filesystem.Remove(s.auth, target); err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	return s.reply(ftp.NewReply(250, "Deleted"))
}

func (s *session) rmd(arg string) error {
	target, err := s.srv.cfg.Filesystem.Resolve(s.auth, s.cwd, arg)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	if err := s.srv.cfg.Filesystem.Remove(s.auth, target); err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	return s.reply(ftp.NewReply(250, "Removed"))
}

func (s *session) mkd(arg string) error {
	target, err := s.srv.cfg.Filesystem.Resolve(s.auth, s.cwd, arg)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	if err := s.srv.cfg.Filesystem.Mkdir(s.auth, target); err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	return s.reply(ftp.NewReply(ftp.CodePathCreated, fmt.Sprintf("%q created", target)))
}

func (s *session) rnfr(arg string) error {
	target, err := s.srv.cfg.Filesystem.Resolve(s.auth, s.cwd, arg)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	s.renameTo = target
	return s.reply(ftp.NewReply(350, "RNFR accepted"))
}

func (s *session) rnto(arg string) error {
	if s.renameTo == "" {
		return s.reply(ftp.NewReply(ftp.CodeSyntaxError, "Bad sequence"))
	}
	target, err := s.srv.cfg.Filesystem.Resolve(s.auth, s.cwd, arg)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	if err := s.srv.cfg.Filesystem.Rename(s.auth, s.renameTo, target); err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	s.renameTo = ""
	return s.reply(ftp.NewReply(250, "Rename ok"))
}

func (s *session) rest2(arg string) error {
	off, err := strconv.ParseInt(strings.TrimSpace(arg), 10, 64)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeSyntaxError, "Bad REST"))
	}
	s.rest = off
	return s.reply(ftp.NewReply(350, "Restart ok"))
}

func (s *session) size(arg string) error {
	target, err := s.srv.cfg.Filesystem.Resolve(s.auth, s.cwd, arg)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	st, err := s.srv.cfg.Filesystem.Stat(s.auth, target)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	return s.reply(ftp.NewReply(ftp.CodeFileStatus, strconv.FormatInt(st.Size, 10)))
}

func (s *session) mdtm(arg string) error {
	target, err := s.srv.cfg.Filesystem.Resolve(s.auth, s.cwd, arg)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	st, err := s.srv.cfg.Filesystem.Stat(s.auth, target)
	if err != nil {
		return s.reply(ftp.NewReply(ftp.CodeFileUnavailable, err.Error()))
	}
	return s.reply(ftp.NewReply(ftp.CodeFileStatus, st.ModTime.UTC().Format("20060102150405")))
}

func (s *session) auth2(_ string) error {
	return s.reply(ftp.NewReply(ftp.CodeCommandNotImpl, "AUTH not supported on this server"))
}

func (s *session) prot(arg string) error {
	switch strings.ToUpper(arg) {
	case "C":
		s.dataTLS = false
	case "P":
		s.dataTLS = true
	default:
		return s.reply(ftp.NewReply(ftp.CodeSyntaxError, "Bad PROT"))
	}
	return s.reply(ftp.NewReply(200, "PROT "+strings.ToUpper(arg)))
}

func (s *session) acceptData() (net.Conn, error) {
	if s.passive == nil {
		return nil, fmt.Errorf("no data channel set up")
	}
	if s.passive.ln != nil {
		conn, err := s.passive.ln.Accept()
		s.passive = nil
		return conn, err
	}
	if s.passive.remote != "" {
		conn, err := net.Dial("tcp", s.passive.remote)
		s.passive = nil
		return conn, err
	}
	return nil, fmt.Errorf("no data channel")
}

type passiveListener struct {
	ln     net.Listener
	remote string
}

func parsePORT(arg string) (host string, port int, err error) {
	parts := strings.Split(arg, ",")
	if len(parts) != 6 {
		return "", 0, fmt.Errorf("bad PORT")
	}
	host = strings.Join(parts[:4], ".")
	p1, err1 := strconv.Atoi(strings.TrimSpace(parts[4]))
	p2, err2 := strconv.Atoi(strings.TrimSpace(parts[5]))
	if err1 != nil || err2 != nil {
		return "", 0, fmt.Errorf("bad PORT")
	}
	return host, p1*256 + p2, nil
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func formatListEntry(e fs.Entry) string {
	mode := "-rw-r--r--"
	if e.IsDir {
		mode = "drwxr-xr-x"
	}
	return fmt.Sprintf("%s   1 %-8s %-8s %12d %s %s",
		mode, e.Owner, e.Group, e.Size,
		e.ModTime.Format("Jan _2 15:04"),
		e.Name,
	)
}
