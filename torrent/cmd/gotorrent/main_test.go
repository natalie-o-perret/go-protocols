package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/natalie-o-perret/go-protocols/torrent/bencode"
	"github.com/natalie-o-perret/go-protocols/torrent/metainfo"
)

func TestJoinPath(t *testing.T) {
	tests := []struct {
		want  string
		parts []string
	}{
		{want: "a/b/c", parts: []string{"a", "b", "c"}},
		{want: "foo", parts: []string{"foo"}},
		{want: "", parts: []string{}},
	}
	for _, tc := range tests {
		if got := joinPath(tc.parts); got != tc.want {
			t.Errorf("joinPath(%v) = %q, want %q", tc.parts, got, tc.want)
		}
	}
}

func TestRunInfoMissingFile(t *testing.T) {
	if err := runInfo([]string{"/nonexistent/file.torrent"}); err == nil {
		t.Fatal("want error for missing file, got nil")
	}
}

func TestRunInfoInvalidBencode(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "*.torrent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("not-bencode"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := runInfo([]string{f.Name()}); err == nil {
		t.Fatal("want error for invalid bencode, got nil")
	}
}

func buildMinimalTorrent(t *testing.T, announce, comment, createdBy string, multiFile bool) []byte {
	t.Helper()
	pieces := strings.Repeat("\x00", 20)
	infoDict := map[string]any{
		"piece length": int64(524288),
		"pieces":       pieces,
	}
	if multiFile {
		infoDict["name"] = "mydir"
		infoDict["files"] = []any{
			map[string]any{
				"length": int64(1024),
				"path":   []any{"sub", "file.txt"},
			},
		}
	} else {
		infoDict["name"] = "test.iso"
		infoDict["length"] = int64(524288)
	}
	top := map[string]any{"info": infoDict}
	if announce != "" {
		top["announce"] = announce
	}
	if comment != "" {
		top["comment"] = comment
	}
	if createdBy != "" {
		top["created by"] = createdBy
	}
	s, err := bencode.EncodeToString(top)
	if err != nil {
		t.Fatalf("buildMinimalTorrent: %v", err)
	}
	return []byte(s)
}

func writeTorrentFile(t *testing.T, data []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.torrent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return f.Name()
}

func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	return buf.String()
}

func TestRunInfoSingleFile(t *testing.T) {
	path := writeTorrentFile(t, buildMinimalTorrent(t, "http://tracker.example.com/announce", "", "", false))
	var runErr error
	out := captureStdout(func() {
		runErr = runInfo([]string{path})
	})
	if runErr != nil {
		t.Fatalf("runInfo: %v", runErr)
	}
	if !strings.Contains(out, "test.iso") {
		t.Errorf("output missing torrent name: %q", out)
	}
	if !strings.Contains(out, "http://tracker.example.com/announce") {
		t.Errorf("output missing announce URL: %q", out)
	}
}

func TestRunInfoWithMetadata(t *testing.T) {
	path := writeTorrentFile(t, buildMinimalTorrent(t, "http://tracker.example.com/announce", "test comment", "me", false))
	var runErr error
	out := captureStdout(func() {
		runErr = runInfo([]string{path})
	})
	if runErr != nil {
		t.Fatalf("runInfo: %v", runErr)
	}
	if !strings.Contains(out, "test comment") {
		t.Errorf("output missing comment: %q", out)
	}
	if !strings.Contains(out, "me") {
		t.Errorf("output missing createdBy: %q", out)
	}
}

func TestRunInfoMultiFile(t *testing.T) {
	path := writeTorrentFile(t, buildMinimalTorrent(t, "http://tracker.example.com/announce", "", "", true))
	var runErr error
	out := captureStdout(func() {
		runErr = runInfo([]string{path})
	})
	if runErr != nil {
		t.Fatalf("runInfo: %v", runErr)
	}
	if !strings.Contains(out, "sub/file.txt") {
		t.Errorf("output missing file path: %q", out)
	}
	if !strings.Contains(out, "Files:") {
		t.Errorf("output missing Files: section: %q", out)
	}
}

func TestRunDownloadEmptyTorrent(t *testing.T) {
	encoded, err := bencode.EncodeToString(map[string]any{
		"info": map[string]any{
			"length":       int64(0),
			"name":         "empty.bin",
			"piece length": int64(16384),
			"pieces":       "",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	torrentPath := writeTorrentFile(t, []byte(encoded))
	workDir := t.TempDir()
	t.Chdir(workDir)
	destination := filepath.Join(workDir, "empty.bin")

	var runErr error
	out := captureStdout(func() {
		runErr = runDownload([]string{torrentPath})
	})
	if runErr != nil {
		t.Fatalf("runDownload: %v", runErr)
	}
	if !strings.Contains(out, "Downloaded empty.bin") {
		t.Fatalf("output = %q", out)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("output size = %d, want 0", info.Size())
	}
}

func TestRunDownloadErrors(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"-bad-flag"},
		{"/nonexistent/file.torrent"},
	} {
		if err := runDownload(args); err == nil {
			t.Fatalf("runDownload(%q) succeeded", args)
		}
	}
	if err := runDownload([]string{writeTorrentFile(t, []byte("not-bencode"))}); err == nil {
		t.Fatal("runDownload accepted invalid metainfo")
	}
}

func TestRunDownloadRejectsUnsafeDefaultName(t *testing.T) {
	encoded, err := bencode.EncodeToString(map[string]any{
		"info": map[string]any{
			"length":       int64(0),
			"name":         "../escape",
			"piece length": int64(16384),
			"pieces":       "",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runDownload([]string{writeTorrentFile(t, []byte(encoded))}); err == nil {
		t.Fatal("runDownload accepted unsafe torrent name")
	}
}

func TestRunDownloadRejectsExistingOutput(t *testing.T) {
	encoded, err := bencode.EncodeToString(map[string]any{
		"info": map[string]any{
			"length":       int64(0),
			"name":         "empty.bin",
			"piece length": int64(16384),
			"pieces":       "",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "existing")
	if err := os.WriteFile(destination, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runDownload([]string{"-o", destination, writeTorrentFile(t, []byte(encoded))}); err == nil {
		t.Fatal("runDownload overwrote existing output")
	}
}

func TestRunDownloadRemovesPartialOutput(t *testing.T) {
	torrentPath := writeTorrentFile(t, buildMinimalTorrent(t, "", "", "", false))
	destination := filepath.Join(t.TempDir(), "payload")
	if err := runDownload([]string{"-o", destination, torrentPath}); err == nil {
		t.Fatal("runDownload succeeded without a tracker")
	}
	for _, path := range []string{destination, destination + ".part"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("partial output %q remains: %v", path, err)
		}
	}
}

func TestFileStoreSpansFiles(t *testing.T) {
	meta := &metainfo.MetaInfo{Info: metainfo.Info{Files: []metainfo.FileInfo{
		{Path: []string{"one"}, Length: 3},
		{Path: []string{"sub", "two"}, Length: 4},
	}}}
	destination := filepath.Join(t.TempDir(), "files")
	store, temporary, err := createOutput(context.Background(), meta, destination)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := store.WriteAt([]byte("abcdefg"), 0); err != nil || n != 7 {
		t.Fatalf("WriteAt = %d, %v", n, err)
	}
	if _, err := store.WriteAt([]byte("x"), 7); err == nil {
		t.Fatal("WriteAt accepted data past the output boundary")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	one, err := os.ReadFile(filepath.Join(temporary, "one"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := os.ReadFile(filepath.Join(temporary, "sub", "two"))
	if err != nil {
		t.Fatal(err)
	}
	if string(one) != "abc" || string(two) != "defg" {
		t.Fatalf("files contain %q and %q", one, two)
	}
}

func TestCreateOutputRejectsTraversal(t *testing.T) {
	meta := &metainfo.MetaInfo{Info: metainfo.Info{Files: []metainfo.FileInfo{
		{Path: []string{"valid"}, Length: 1},
		{Path: []string{"..", "escape"}, Length: 1},
	}}}
	destination := filepath.Join(t.TempDir(), "files")
	_, temporary, err := createOutput(context.Background(), meta, destination)
	if err == nil {
		t.Fatal("createOutput accepted path traversal")
	}
	if temporary != "" {
		t.Fatalf("temporary = %q, want empty", temporary)
	}
	if _, err := os.Stat(destination + ".part"); !os.IsNotExist(err) {
		t.Fatalf("temporary output remains after error: %v", err)
	}
}

func TestCreateOutputHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	destination := filepath.Join(t.TempDir(), "file")
	_, temporary, err := createOutput(ctx, &metainfo.MetaInfo{}, destination)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("createOutput error = %v, want context.Canceled", err)
	}
	if temporary != "" {
		t.Fatalf("temporary = %q, want empty", temporary)
	}
}
