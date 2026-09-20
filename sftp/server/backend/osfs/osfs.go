// Package osfs is an OS-backed implementation of server.Backend.
// All operations are constrained to a root directory; the server
// is responsible for authenticating the user before reaching this
// package.
package osfs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/natalie-o-perret/go-sftp/server"
	"github.com/natalie-o-perret/go-sftp/sftp"
)

// FS is an OS-backed SFTP backend.
type FS struct {
	root string

	mu      sync.Mutex
	handles map[server.FileHandle]*os.File
	next    int
}

// New returns an FS rooted at root.
func New(root string) *FS {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return &FS{root: abs, handles: map[server.FileHandle]*os.File{}}
}

func (f *FS) join(path string) string {
	rel := strings.TrimPrefix(path, "/")
	return filepath.Join(f.root, rel)
}

func (f *FS) newHandle(file *os.File) server.FileHandle {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	h := server.FileHandle("h" + strconv.Itoa(f.next))
	f.handles[h] = file
	return h
}

func (f *FS) getHandle(h server.FileHandle) (*os.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	file, ok := f.handles[h]
	if !ok {
		return nil, errors.New("invalid handle")
	}
	return file, nil
}

func (f *FS) drop(h server.FileHandle) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.handles, h)
}

func statToFileStat(name string, fi os.FileInfo) server.FileStat {
	return server.FileStat{
		Name:  name,
		Size:  uint64(fi.Size()),
		Mode:  uint32(fi.Mode().Perm()),
		IsDir: fi.IsDir(),
		Mtime: fi.ModTime().Unix(),
		UID:   0,
		GID:   0,
	}
}

// Stat implements server.Backend.
func (f *FS) Stat(path string) (server.FileStat, error) {
	fi, err := os.Stat(f.join(path))
	if err != nil {
		return server.FileStat{}, err
	}
	return statToFileStat(fi.Name(), fi), nil
}

// Lstat implements server.Backend.
func (f *FS) Lstat(path string) (server.FileStat, error) {
	fi, err := os.Lstat(f.join(path))
	if err != nil {
		return server.FileStat{}, err
	}
	return statToFileStat(fi.Name(), fi), nil
}

// Open implements server.Backend. The flags argument is a bitmask
// of the sftp.OpenRead/Write/Append/Create/Trunc/Excl constants.
func (f *FS) Open(path string, flags uint32) (server.FileHandle, error) {
	full := f.join(path)
	oflags := 0
	if flags&sftp.OpenRead != 0 {
		oflags |= os.O_RDONLY
	}
	if flags&sftp.OpenWrite != 0 {
		oflags |= os.O_WRONLY
	}
	if flags&sftp.OpenRead != 0 && flags&sftp.OpenWrite != 0 {
		oflags = os.O_RDWR
	}
	if flags&sftp.OpenAppend != 0 {
		oflags |= os.O_APPEND
	}
	if flags&sftp.OpenCreate != 0 {
		oflags |= os.O_CREATE
	}
	if flags&sftp.OpenTrunc != 0 {
		oflags |= os.O_TRUNC
	}
	if flags&sftp.OpenExcl != 0 {
		oflags |= os.O_EXCL
	}
	file, err := os.OpenFile(full, oflags, 0o644)
	if err != nil {
		return "", err
	}
	return f.newHandle(file), nil
}

// Read implements server.Backend.
func (f *FS) Read(h server.FileHandle, offset uint64, data []byte) (int, error) {
	file, err := f.getHandle(h)
	if err != nil {
		return 0, err
	}
	if _, err := file.Seek(int64(offset), io.SeekStart); err != nil {
		return 0, err
	}
	return file.Read(data)
}

// Write implements server.Backend.
func (f *FS) Write(h server.FileHandle, offset uint64, data []byte) (int, error) {
	file, err := f.getHandle(h)
	if err != nil {
		return 0, err
	}
	if _, err := file.Seek(int64(offset), io.SeekStart); err != nil {
		return 0, err
	}
	return file.Write(data)
}

// Close implements server.Backend.
func (f *FS) Close(h server.FileHandle) error {
	file, err := f.getHandle(h)
	if err != nil {
		return err
	}
	f.drop(h)
	return file.Close()
}

// Readdir implements server.Backend.
func (f *FS) Readdir(path string) ([]server.FileStat, error) {
	full := f.join(path)
	entries, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}
	out := make([]server.FileStat, 0, len(entries))
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, statToFileStat(e.Name(), fi))
	}
	return out, nil
}

// Mkdir implements server.Backend.
func (f *FS) Mkdir(path string) error {
	return os.MkdirAll(f.join(path), 0o755)
}

// Rmdir implements server.Backend.
func (f *FS) Rmdir(path string) error {
	return os.RemoveAll(f.join(path))
}

// Remove implements server.Backend.
func (f *FS) Remove(path string) error {
	return os.Remove(f.join(path))
}

// Rename implements server.Backend.
func (f *FS) Rename(from, to string) error {
	return os.Rename(f.join(from), f.join(to))
}
