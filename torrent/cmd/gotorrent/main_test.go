package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/natalie-o-perret/go-torrent/bencode"
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
