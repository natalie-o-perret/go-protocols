// Package fs defines the virtual filesystem interface used by the
// FTP server and provides two reference implementations: a
// memory-backed filesystem (useful for tests) and an OS-backed
// filesystem rooted at a directory.
package fs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/natalie-o-perret/go-ftp/server/auth"
)

// Entry is a single directory entry returned by List.
type Entry struct {
	Name    string
	Size    int64
	IsDir   bool
	ModTime time.Time
	Owner   string
	Group   string
}

// Filesystem is the virtual filesystem interface. All paths are
// absolute and rooted at the user's home; the caller is expected to
// have resolved relative paths through Resolve.
type Filesystem interface {
	Resolve(u auth.User, cwd, arg string) (string, error)
	Stat(u auth.User, path string) (Entry, error)
	List(u auth.User, path string) ([]Entry, error)
	Mkdir(u auth.User, path string) error
	Remove(u auth.User, path string) error
	Rename(u auth.User, from, to string) error
	SendFile(u auth.User, path string, w io.Writer, offset int64) error
	RecvFile(u auth.User, path string, r io.Reader) error
	AppendFile(u auth.User, path string, r io.Reader) error
}

// pathError is a convenience for returning path-related errors.
func pathError(op, path string, err error) error {
	if err == nil {
		return nil
	}
	return &fsError{op: op, path: path, err: err}
}

type fsError struct {
	op, path string
	err      error
}

func (e *fsError) Error() string { return e.op + " " + e.path + ": " + e.err.Error() }
func (e *fsError) Unwrap() error { return e.err }

// OS is an OS-backed filesystem rooted at root.
type OS struct {
	root string
}

// NewOS returns an OS filesystem rooted at the given directory.
func NewOS(root string) *OS {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return &OS{root: abs}
}

func (o *OS) full(u auth.User, p string) string {
	if u.Home != "" {
		rel := strings.TrimPrefix(p, "/")
		return filepath.Join(o.root, u.Home, rel)
	}
	rel := strings.TrimPrefix(p, "/")
	return filepath.Join(o.root, rel)
}

func (o *OS) Resolve(_ auth.User, cwd, arg string) (string, error) {
	if arg == "" {
		return cwd, nil
	}
	if !strings.HasPrefix(arg, "/") {
		arg = cwd + "/" + arg
	}
	clean := pathClean(arg)
	if !strings.HasPrefix(clean, "/") {
		return "", errors.New("bad path")
	}
	return clean, nil
}

func (o *OS) Stat(u auth.User, p string) (Entry, error) {
	st, err := os.Stat(o.full(u, p))
	if err != nil {
		return Entry{}, pathError("stat", p, err)
	}
	return entryFromInfo(st), nil
}

func (o *OS) List(u auth.User, p string) ([]Entry, error) {
	f, err := os.Open(o.full(u, p))
	if err != nil {
		return nil, pathError("list", p, err)
	}
	defer func() { _ = f.Close() }()
	infos, err := f.Readdir(-1)
	if err != nil {
		return nil, pathError("list", p, err)
	}
	entries := make([]Entry, 0, len(infos))
	for _, fi := range infos {
		entries = append(entries, entryFromInfo(fi))
	}
	return entries, nil
}

func (o *OS) Mkdir(u auth.User, p string) error {
	return pathError("mkdir", p, os.MkdirAll(o.full(u, p), 0o755))
}

func (o *OS) Remove(u auth.User, p string) error {
	return pathError("remove", p, os.RemoveAll(o.full(u, p)))
}

func (o *OS) Rename(u auth.User, from, to string) error {
	return pathError("rename", from+"->"+to, os.Rename(o.full(u, from), o.full(u, to)))
}

func (o *OS) SendFile(u auth.User, p string, w io.Writer, offset int64) error {
	f, err := os.Open(o.full(u, p))
	if err != nil {
		return pathError("retr", p, err)
	}
	defer func() { _ = f.Close() }()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}
	_, err = io.Copy(w, f)
	return err
}

func (o *OS) RecvFile(u auth.User, p string, r io.Reader) error {
	f, err := os.Create(o.full(u, p))
	if err != nil {
		return pathError("stor", p, err)
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(f, r)
	return err
}

func (o *OS) AppendFile(u auth.User, p string, r io.Reader) error {
	f, err := os.OpenFile(o.full(u, p), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return pathError("appe", p, err)
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(f, r)
	return err
}

func entryFromInfo(fi os.FileInfo) Entry {
	return Entry{
		Name:    fi.Name(),
		Size:    fi.Size(),
		IsDir:   fi.IsDir(),
		ModTime: fi.ModTime(),
		Owner:   "-",
		Group:   "-",
	}
}

func pathClean(p string) string {
	out := strings.Builder{}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
		case "..":
			// pop one segment if possible
			s := out.String()
			if i := strings.LastIndex(s, "/"); i >= 0 {
				out.Reset()
				out.WriteString(s[:i])
			}
		default:
			out.WriteString("/")
			out.WriteString(seg)
		}
	}
	if out.Len() == 0 {
		return "/"
	}
	return out.String()
}
