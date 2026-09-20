package integration_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/natalie-o-perret/go-sftp/client"
	"github.com/natalie-o-perret/go-sftp/server"
	"github.com/natalie-o-perret/go-sftp/server/backend/osfs"
	"github.com/natalie-o-perret/go-sftp/sftp"
)

// pipe returns two io.ReadWriteClosers connected to each other. The
// test feeds one side to a Server, the other to a Client.
func pipe() (a, b net.Conn) {
	c1, c2 := net.Pipe()
	return c1, c2
}

func runServer(t *testing.T, root string) net.Conn {
	t.Helper()
	serverSide, clientSide := pipe()
	srv := server.New(osfs.New(root))
	go func() { _ = srv.Serve(serverSide) }()
	return clientSide
}

func TestStatAndRead(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cli := runServer(t, root)
	defer func() { _ = cli.Close() }()
	c, err := client.New(cli)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = c.Stat(ctx, "/hello.txt")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	f, err := c.Open(ctx, "/hello.txt")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("hi\n")) {
		t.Fatalf("got %q", got)
	}
}

func TestCreateWriteRead(t *testing.T) {
	root := t.TempDir()
	cli := runServer(t, root)
	defer func() { _ = cli.Close() }()
	c, err := client.New(cli)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	f, err := c.Create(ctx, "/new.txt")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.Write([]byte("written\n")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "written\n" {
		t.Fatalf("got %q", got)
	}
}

func TestReadDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	cli := runServer(t, root)
	defer func() { _ = cli.Close() }()
	c, err := client.New(cli)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	entries, err := c.ReadDir(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
}

func TestMkdirAndRemove(t *testing.T) {
	root := t.TempDir()
	cli := runServer(t, root)
	defer func() { _ = cli.Close() }()
	c, err := client.New(cli)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.Mkdir(ctx, "/d", sftp.FileAttr{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "d")); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(ctx, "/d"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "d")); !os.IsNotExist(err) {
		t.Fatalf("expected not exist, got %v", err)
	}
}
