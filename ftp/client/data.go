package client

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/natalie-o-perret/go-ftp/ftp"
)

// DataChannel describes a passive-mode data channel returned by the
// server. The caller dials addr and performs the transfer; once the
// transfer is complete the caller must invoke the returned Done
// function so the server's final 226 reply is consumed on the
// control channel.
type DataChannel struct {
	Addr string
	Done func() (ftp.Reply, error)
}

// openDataPassive issues PASV or EPSV, dials the returned address,
// and returns a DataChannel. The closer drains the final
// data-completion reply.
func (c *Client) openDataPassive() (*DataChannel, error) {
	port, err := c.Epsv()
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(c.host, strconv.Itoa(port))
	dc := &DataChannel{Addr: addr}
	dc.Done = func() (ftp.Reply, error) {
		reply, err := c.readReply()
		if err != nil {
			return ftp.Reply{}, err
		}
		if !reply.Positive() {
			return reply, reply
		}
		return reply, nil
	}
	return dc, nil
}

// openDataPassive opens a passive data channel and dials it.
func (c *Client) dialDataPassive(timeout time.Duration) (*DataChannel, net.Conn, error) {
	dc, err := c.openDataPassive()
	if err != nil {
		return nil, nil, err
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	conn, err := net.DialTimeout("tcp", dc.Addr, timeout)
	if err != nil {
		return nil, nil, fmt.Errorf("ftp: dial data: %w", err)
	}
	return dc, conn, nil
}

// Retr issues RETR for path, opens a data channel, and returns a
// Reader that reads the file's bytes followed by EOF when the
// transfer is complete. The caller must close the underlying data
// connection and then call Done to drain the final 226.
func (c *Client) Retr(path string) (rc io.ReadCloser, done func() (ftp.Reply, error), err error) {
	reply, err := c.Do(ftp.CmdRETR, path)
	if err != nil {
		return nil, nil, err
	}
	if reply.Code != ftp.CodeDataOpen {
		return nil, nil, reply
	}
	dc, conn, err := c.dialDataPassive(0)
	if err != nil {
		return nil, nil, err
	}
	return &dataReader{conn: conn}, dc.Done, nil
}

// Stor issues STOR for path, opens a data channel, and returns a
// Writer that the caller can push file bytes to. After the caller
// closes the writer, Done must be invoked to drain the final 226.
func (c *Client) Stor(path string) (wc io.WriteCloser, done func() (ftp.Reply, error), err error) {
	reply, err := c.Do(ftp.CmdSTOR, path)
	if err != nil {
		return nil, nil, err
	}
	if reply.Code != ftp.CodeDataOpen {
		return nil, nil, reply
	}
	dc, conn, err := c.dialDataPassive(0)
	if err != nil {
		return nil, nil, err
	}
	return &dataWriter{conn: conn}, dc.Done, nil
}

// List issues LIST for path (or the current directory if path is
// empty) and returns the raw directory listing bytes. Use
// ParseList from a separate sub-package to interpret the result.
func (c *Client) List(path string) ([]byte, error) {
	arg := ""
	if path != "" {
		arg = path
	}
	reply, err := c.Do(ftp.CmdLIST, arg)
	if err != nil {
		return nil, err
	}
	if reply.Code != ftp.CodeDataOpen {
		return nil, reply
	}
	dc, conn, err := c.dialDataPassive(0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			break
		}
	}
	if _, err := dc.Done(); err != nil {
		return nil, err
	}
	return buf, nil
}

type dataReader struct {
	conn net.Conn
}

func (d *dataReader) Read(p []byte) (int, error) { return d.conn.Read(p) }
func (d *dataReader) Close() error               { return d.conn.Close() }

type dataWriter struct {
	conn net.Conn
}

func (d *dataWriter) Write(p []byte) (int, error) { return d.conn.Write(p) }
func (d *dataWriter) Close() error                { return d.conn.Close() }
