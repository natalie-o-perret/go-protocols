package metainfo_test

import (
	"bytes"
	"crypto/sha1"
	"strings"
	"testing"

	"github.com/natalie-o-perret/go-torrent/bencode"
	"github.com/natalie-o-perret/go-torrent/metainfo"
)

func buildTorrent(t *testing.T, announce string, infoDict map[string]any) []byte {
	t.Helper()
	top := map[string]any{"info": infoDict}
	if announce != "" {
		top["announce"] = announce
	}
	s, err := bencode.EncodeToString(top)
	if err != nil {
		t.Fatalf("buildTorrent: encode: %v", err)
	}
	return []byte(s)
}

func zeroHashes(n int) string {
	return strings.Repeat(strings.Repeat("\x00", 20), n)
}

func TestDecodeSingleFile(t *testing.T) {
	infoDict := map[string]any{
		"name":         "test.iso",
		"piece length": int64(524288),
		"pieces":       zeroHashes(2),
		"length":       int64(1048576),
	}
	data := buildTorrent(t, "http://tracker.example.com/announce", infoDict)

	m, err := metainfo.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if m.Info.Name != "test.iso" {
		t.Errorf("Name = %q, want test.iso", m.Info.Name)
	}
	if m.Info.Length != 1048576 {
		t.Errorf("Length = %d, want 1048576", m.Info.Length)
	}
	if m.Info.PieceLength != 524288 {
		t.Errorf("PieceLength = %d, want 524288", m.Info.PieceLength)
	}
	if m.Info.PieceCount() != 2 {
		t.Errorf("PieceCount = %d, want 2", m.Info.PieceCount())
	}
	if m.Info.TotalLength() != 1048576 {
		t.Errorf("TotalLength = %d, want 1048576", m.Info.TotalLength())
	}
	if m.Announce != "http://tracker.example.com/announce" {
		t.Errorf("Announce = %q", m.Announce)
	}
	if len(m.Info.Files) != 0 {
		t.Errorf("Files should be nil for single-file torrent, got %d", len(m.Info.Files))
	}
}

func TestDecodeMultiFile(t *testing.T) {
	infoDict := map[string]any{
		"name":         "album",
		"piece length": int64(262144),
		"pieces":       zeroHashes(1),
		"files": []any{
			map[string]any{
				"length": int64(131072),
				"path":   []any{"track01.flac"},
			},
			map[string]any{
				"length": int64(131072),
				"path":   []any{"track02.flac"},
			},
		},
	}
	data := buildTorrent(t, "http://tracker.example.com/announce", infoDict)

	m, err := metainfo.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if m.Info.Name != "album" {
		t.Errorf("Name = %q, want album", m.Info.Name)
	}
	if len(m.Info.Files) != 2 {
		t.Fatalf("len(Files) = %d, want 2", len(m.Info.Files))
	}
	if m.Info.Files[0].Path[0] != "track01.flac" {
		t.Errorf("Files[0].Path[0] = %q", m.Info.Files[0].Path[0])
	}
	if m.Info.TotalLength() != 262144 {
		t.Errorf("TotalLength = %d, want 262144", m.Info.TotalLength())
	}
}

func TestInfoHash(t *testing.T) {
	infoDict := map[string]any{
		"name":         "test.iso",
		"piece length": int64(524288),
		"pieces":       zeroHashes(1),
		"length":       int64(524288),
	}
	data := buildTorrent(t, "http://tracker.example.com/announce", infoDict)

	m, err := metainfo.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	infoEncoded, err := bencode.EncodeToString(infoDict)
	if err != nil {
		t.Fatalf("encode info: %v", err)
	}
	want := sha1.Sum([]byte(infoEncoded))

	if m.InfoHash != want {
		t.Errorf("InfoHash = %s, want %x", m.InfoHash, want)
	}
}

func TestTrackers(t *testing.T) {
	infoDict := map[string]any{
		"name":         "test.iso",
		"piece length": int64(524288),
		"pieces":       zeroHashes(1),
		"length":       int64(524288),
	}
	top := map[string]any{
		"info":     infoDict,
		"announce": "http://primary.example.com/announce",
		"announce-list": []any{
			[]any{"http://primary.example.com/announce"},
			[]any{"http://secondary.example.com/announce", "http://tertiary.example.com/announce"},
		},
	}
	s, err := bencode.EncodeToString(top)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	m, err := metainfo.Decode(strings.NewReader(s))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	trackers := m.Trackers()
	if trackers[0] != "http://primary.example.com/announce" {
		t.Errorf("trackers[0] = %q, want primary", trackers[0])
	}
	found := make(map[string]int)
	for _, u := range trackers {
		found[u]++
	}
	if found["http://primary.example.com/announce"] != 1 {
		t.Errorf("primary appeared %d times, want 1 (dedup)", found["http://primary.example.com/announce"])
	}
	if len(trackers) != 3 {
		t.Errorf("len(trackers) = %d, want 3", len(trackers))
	}
}

func TestDecodeMissingInfo(t *testing.T) {
	s, err := bencode.EncodeToString(map[string]any{"announce": "http://x.example.com"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	_, err = metainfo.Decode(strings.NewReader(s))
	if err == nil {
		t.Error("want error for missing 'info' key, got nil")
	}
}

func TestDecodeMissingPieces(t *testing.T) {
	infoDict := map[string]any{
		"name":         "test.iso",
		"piece length": int64(524288),
		"length":       int64(524288),
	}
	data := buildTorrent(t, "", infoDict)
	_, err := metainfo.Decode(bytes.NewReader(data))
	if err == nil {
		t.Error("want error for missing 'pieces' key, got nil")
	}
}

func TestDecodeInvalidPiecesLength(t *testing.T) {
	infoDict := map[string]any{
		"name":         "test.iso",
		"piece length": int64(524288),
		"pieces":       "abc",
		"length":       int64(524288),
	}
	data := buildTorrent(t, "", infoDict)
	_, err := metainfo.Decode(bytes.NewReader(data))
	if err == nil {
		t.Error("want error for pieces length not multiple of 20, got nil")
	}
}

func TestHashString(t *testing.T) {
	var h metainfo.Hash
	h[0] = 0xde
	h[1] = 0xad
	s := h.String()
	if !strings.HasPrefix(s, "dead") {
		t.Errorf("Hash.String() = %q, want prefix dead", s)
	}
	if len(s) != 40 {
		t.Errorf("Hash.String() len = %d, want 40", len(s))
	}
}
