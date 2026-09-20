// Package client implements an SFTP client. It is built on top of
// the SSH File Transfer Protocol subsystem: a caller supplies an
// established SSH channel configured as the "sftp" subsystem and
// the client takes over the SFTP protocol on that channel.
//
// The client supports a focused set of high-level operations:
// Stat, ReadDir, Open+Read, Create+Write, Mkdir, Remove, Rename.
package client

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/natalie-o-perret/go-sftp/sftp"
)

// Channel is the minimum SSH channel interface the client needs.
// *ssh.Channel from golang.org/x/crypto/ssh satisfies it.
type Channel interface {
	io.ReadWriteCloser
}

// Client is a single SFTP session. It is safe to use from
// multiple goroutines; calls block until their reply is received.
type Client struct {
	ch        Channel
	nextID    func() sftp.RequestID
	pendMu    sync.Mutex
	pending   map[sftp.RequestID]chan sftp.Packet
	readErr   error
	readDone  chan struct{}
	closeOnce sync.Once
}

// New negotiates the SFTP version on ch and returns a ready Client.
func New(ch Channel) (*Client, error) {
	c := &Client{
		ch:       ch,
		nextID:   sftp.NewRequestIDSource(),
		pending:  make(map[sftp.RequestID]chan sftp.Packet),
		readDone: make(chan struct{}),
	}
	if err := sftp.WritePacket(ch, sftp.TypeInit, sftp.AppendUint32(nil, sftp.Version)); err != nil {
		return nil, err
	}
	pkt, err := sftp.ReadPacket(ch)
	if err != nil {
		return nil, err
	}
	if pkt.Type != sftp.TypeVersion {
		return nil, fmt.Errorf("sftp: expected VERSION, got %d", pkt.Type)
	}
	srvVer, err := sftp.ReadInit(pkt)
	if err != nil {
		return nil, err
	}
	if srvVer.Version != sftp.Version {
		return nil, fmt.Errorf("sftp: unsupported server version %d", srvVer.Version)
	}
	go c.readLoop()
	return c, nil
}

// Close closes the underlying channel and unblocks pending callers.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.readDone)
	})
	return c.ch.Close()
}

// readLoop drains incoming packets and dispatches them to the
// waiting request.
func (c *Client) readLoop() {
	for {
		pkt, err := sftp.ReadPacket(c.ch)
		if err != nil {
			c.pendMu.Lock()
			c.readErr = err
			for _, ch := range c.pending {
				close(ch)
			}
			c.pending = nil
			c.pendMu.Unlock()
			return
		}
		if len(pkt.Payload) < 4 {
			continue
		}
		id := sftp.RequestID(binary.BigEndian.Uint32(pkt.Payload[:4]))
		c.pendMu.Lock()
		ch, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
		}
		c.pendMu.Unlock()
		if ok {
			ch <- pkt
			close(ch)
		}
	}
}

func (c *Client) do(ctx context.Context, t sftp.Type, payload []byte) (sftp.Packet, error) {
	id := c.nextID()
	reply := make(chan sftp.Packet, 1)
	c.pendMu.Lock()
	c.pending[id] = reply
	c.pendMu.Unlock()
	full := sftp.AppendUint32(nil, uint32(id))
	full = append(full, payload...)
	if err := sftp.WritePacket(c.ch, t, full); err != nil {
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
		return sftp.Packet{}, err
	}
	select {
	case pkt, ok := <-reply:
		if !ok {
			return sftp.Packet{}, c.readErr
		}
		return pkt, nil
	case <-ctx.Done():
		return sftp.Packet{}, ctx.Err()
	case <-c.readDone:
		return sftp.Packet{}, io.ErrClosedPipe
	}
}

// open sends OPEN and returns the file handle.
func (c *Client) open(ctx context.Context, path string, pFlags uint32) (string, error) {
	payload := sftp.AppendString(nil, path)
	payload = sftp.AppendUint32(payload, pFlags)
	pkt, err := c.do(ctx, sftp.TypeOpen, payload)
	if err != nil {
		return "", err
	}
	if pkt.Type == sftp.TypeStatus {
		_, code, msg, _ := sftp.DecodeStatus(pkt.Payload)
		return "", &StatusError{Code: code, Msg: msg}
	}
	if pkt.Type != sftp.TypeHandle {
		return "", fmt.Errorf("sftp: OPEN: unexpected reply %d", pkt.Type)
	}
	handle, _, err := sftp.ReadString(pkt.Payload, 4)
	return handle, err
}

// Open opens path for reading.
func (c *Client) Open(ctx context.Context, path string) (*File, error) {
	return c.openFile(ctx, path, sftp.OpenRead)
}

// Create creates path for writing (truncate).
func (c *Client) Create(ctx context.Context, path string) (*File, error) {
	return c.openFile(ctx, path, sftp.OpenWrite|sftp.OpenCreate|sftp.OpenTrunc)
}

// Append opens path for writing at the end of the file.
func (c *Client) Append(ctx context.Context, path string) (*File, error) {
	return c.openFile(ctx, path, sftp.OpenWrite|sftp.OpenAppend)
}

func (c *Client) openFile(ctx context.Context, path string, pFlags uint32) (*File, error) {
	handle, err := c.open(ctx, path, pFlags)
	if err != nil {
		return nil, err
	}
	return &File{c: c, handle: handle}, nil
}

// CloseHandle sends CLOSE for handle and returns the server's status.
func (c *Client) CloseHandle(ctx context.Context, handle string) error {
	payload := sftp.AppendString(nil, handle)
	pkt, err := c.do(ctx, sftp.TypeClose, payload)
	if err != nil {
		return err
	}
	_, code, msg, _ := sftp.DecodeStatus(pkt.Payload)
	if code != sftp.StatusOK {
		return &StatusError{Code: code, Msg: msg}
	}
	return nil
}

// Read issues a single READ request for the file at handle starting
// at offset and reading up to len(data) bytes. The number of bytes
// read is returned. A short read plus a status of EOF means the
// caller has reached end of file.
func (c *Client) Read(ctx context.Context, handle string, offset uint64, data []byte) (int, error) {
	payload := sftp.AppendString(nil, handle)
	payload = sftp.AppendUint64(payload, offset)
	payload = sftp.AppendUint32(payload, uint32(len(data)))
	pkt, err := c.do(ctx, sftp.TypeRead, payload)
	if err != nil {
		return 0, err
	}
	if pkt.Type == sftp.TypeStatus {
		_, code, msg, _ := sftp.DecodeStatus(pkt.Payload)
		if code == sftp.StatusEOF {
			return 0, io.EOF
		}
		return 0, &StatusError{Code: code, Msg: msg}
	}
	if pkt.Type != sftp.TypeData {
		return 0, fmt.Errorf("sftp: READ: unexpected reply %d", pkt.Type)
	}
	n, _, err := sftp.ReadString(pkt.Payload, 4)
	if err != nil {
		return 0, err
	}
	return copy(data, n), nil
}

// Write writes data at offset.
func (c *Client) Write(ctx context.Context, handle string, offset uint64, data []byte) (int, error) {
	payload := sftp.AppendString(nil, handle)
	payload = sftp.AppendUint64(payload, offset)
	buf := make([]byte, 0, 8+len(data))
	buf = sftp.AppendUint32(buf, uint32(len(data)))
	buf = append(buf, data...)
	payload = append(payload, buf...)
	pkt, err := c.do(ctx, sftp.TypeWrite, payload)
	if err != nil {
		return 0, err
	}
	_, code, msg, _ := sftp.DecodeStatus(pkt.Payload)
	if code != sftp.StatusOK {
		return 0, &StatusError{Code: code, Msg: msg}
	}
	return len(data), nil
}

// Stat returns the attributes of path. It follows symlinks.
func (c *Client) Stat(ctx context.Context, path string) (sftp.FileAttr, error) {
	return c.stat(ctx, path, sftp.TypeStat)
}

// Lstat returns the attributes of path without following symlinks.
func (c *Client) Lstat(ctx context.Context, path string) (sftp.FileAttr, error) {
	return c.stat(ctx, path, sftp.TypeLstat)
}

func (c *Client) stat(ctx context.Context, path string, t sftp.Type) (sftp.FileAttr, error) {
	payload := sftp.AppendString(nil, path)
	pkt, err := c.do(ctx, t, payload)
	if err != nil {
		return sftp.FileAttr{}, err
	}
	if pkt.Type == sftp.TypeStatus {
		_, code, msg, _ := sftp.DecodeStatus(pkt.Payload)
		return sftp.FileAttr{}, &StatusError{Code: code, Msg: msg}
	}
	a, _, err := sftp.DecodeAttrs(pkt.Payload, 4)
	return a, err
}

// ReadDir lists the contents of dir. The result excludes "." and
// "..".
func (c *Client) ReadDir(ctx context.Context, dir string) ([]DirEntry, error) {
	payload := sftp.AppendString(nil, dir)
	pkt, err := c.do(ctx, sftp.TypeOpendir, payload)
	if err != nil {
		return nil, err
	}
	if pkt.Type == sftp.TypeStatus {
		_, code, msg, _ := sftp.DecodeStatus(pkt.Payload)
		return nil, &StatusError{Code: code, Msg: msg}
	}
	if pkt.Type != sftp.TypeHandle {
		return nil, fmt.Errorf("sftp: OPENDIR: unexpected reply %d", pkt.Type)
	}
	handle, _, err := sftp.ReadString(pkt.Payload, 4)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.CloseHandle(ctx, handle) }()
	var entries []DirEntry
	for {
		payload := sftp.AppendString(nil, handle)
		pkt, err := c.do(ctx, sftp.TypeReaddir, payload)
		if err != nil {
			return nil, err
		}
		if pkt.Type == sftp.TypeStatus {
			_, code, _, _ := sftp.DecodeStatus(pkt.Payload)
			if code == sftp.StatusEOF {
				break
			}
			return nil, &StatusError{Code: code}
		}
		off := 4
		for off < len(pkt.Payload) {
			name, next, err := sftp.ReadString(pkt.Payload, off)
			if err != nil {
				return nil, err
			}
			off = next
			_, next, err = sftp.ReadString(pkt.Payload, off)
			if err != nil {
				return nil, err
			}
			off = next
			a, next, err := sftp.DecodeAttrs(pkt.Payload, off)
			if err != nil {
				return nil, err
			}
			off = next
			if name == "." || name == ".." {
				continue
			}
			entries = append(entries, DirEntry{Name: name, Attr: a})
		}
	}
	return entries, nil
}

// Mkdir creates directory path. attrs is currently advisory and
// should be FileAttr{} for a default set.
func (c *Client) Mkdir(ctx context.Context, path string, attrs sftp.FileAttr) error {
	payload := sftp.AppendString(nil, path)
	payload = append(payload, attrs.Encode()...)
	pkt, err := c.do(ctx, sftp.TypeMkdir, payload)
	if err != nil {
		return err
	}
	_, code, msg, _ := sftp.DecodeStatus(pkt.Payload)
	if code != sftp.StatusOK {
		return &StatusError{Code: code, Msg: msg}
	}
	return nil
}

// Remove removes the file at path.
func (c *Client) Remove(ctx context.Context, path string) error {
	return c.simpleStatus(ctx, sftp.TypeRemove, path)
}

// Rmdir removes the directory at path.
func (c *Client) Rmdir(ctx context.Context, path string) error {
	return c.simpleStatus(ctx, sftp.TypeRmdir, path)
}

// Rename renames from to to.
func (c *Client) Rename(ctx context.Context, from, to string) error {
	payload := sftp.AppendString(nil, from)
	payload = sftp.AppendString(payload, to)
	pkt, err := c.do(ctx, sftp.TypeRename, payload)
	if err != nil {
		return err
	}
	_, code, msg, _ := sftp.DecodeStatus(pkt.Payload)
	if code != sftp.StatusOK {
		return &StatusError{Code: code, Msg: msg}
	}
	return nil
}

func (c *Client) simpleStatus(ctx context.Context, t sftp.Type, path string) error {
	payload := sftp.AppendString(nil, path)
	pkt, err := c.do(ctx, t, payload)
	if err != nil {
		return err
	}
	_, code, msg, _ := sftp.DecodeStatus(pkt.Payload)
	if code != sftp.StatusOK {
		return &StatusError{Code: code, Msg: msg}
	}
	return nil
}

// File is an open file handle on the SFTP server. It implements
// io.ReadWriteCloser.
type File struct {
	c      *Client
	handle string
	off    uint64
}

// Read implements io.Reader.
func (f *File) Read(p []byte) (int, error) {
	n, err := f.c.Read(context.Background(), f.handle, f.off, p)
	f.off += uint64(n)
	return n, err
}

// Write implements io.Writer.
func (f *File) Write(p []byte) (int, error) {
	n, err := f.c.Write(context.Background(), f.handle, f.off, p)
	f.off += uint64(n)
	return n, err
}

// Close releases the server-side handle.
func (f *File) Close() error {
	err := f.c.CloseHandle(context.Background(), f.handle)
	if errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}

// DirEntry is a single directory entry returned by ReadDir.
type DirEntry struct {
	Name string
	Attr sftp.FileAttr
}

// StatusError is the error returned when a STATUS reply has a code
// other than StatusOK.
type StatusError struct {
	Code sftp.StatusCode
	Msg  string
}

// Error implements error.
func (e *StatusError) Error() string {
	return fmt.Sprintf("sftp: status %d: %s", e.Code, e.Msg)
}
