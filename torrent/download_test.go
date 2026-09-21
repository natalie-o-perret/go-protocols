package gotorrent_test

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	gotorrent "github.com/natalie-o-perret/go-protocols/torrent"
	"github.com/natalie-o-perret/go-protocols/torrent/bencode"
	"github.com/natalie-o-perret/go-protocols/torrent/bitfield"
	"github.com/natalie-o-perret/go-protocols/torrent/metainfo"
	"github.com/natalie-o-perret/go-protocols/torrent/peer"
	"github.com/natalie-o-perret/go-protocols/torrent/piece"
)

type bufferAt []byte

func (b bufferAt) WriteAt(data []byte, offset int64) (int, error) {
	if offset < 0 || int(offset)+len(data) > len(b) {
		return 0, fmt.Errorf("write out of range")
	}
	return copy(b[int(offset):], data), nil
}

func TestDownload(t *testing.T) {
	data := bytes.Repeat([]byte("verified torrent data"), 2000)
	pieceLength := int64(piece.BlockSize * 2)
	hashes := make([]metainfo.Hash, 0, 2)
	for offset := int64(0); offset < int64(len(data)); offset += pieceLength {
		end := min(offset+pieceLength, int64(len(data)))
		hashes = append(hashes, sha1.Sum(data[offset:end]))
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	_, portString, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatal(err)
	}

	compactPeer := []byte{127, 0, 0, 1, byte(port >> 8), byte(port)}
	trackerBody, err := bencode.EncodeToString(map[string]any{
		"interval": int64(60),
		"peers":    string(compactPeer),
	})
	if err != nil {
		t.Fatal(err)
	}
	var started, completed atomic.Int32
	trackerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Query().Get("event") {
		case "started":
			started.Add(1)
		case "completed":
			completed.Add(1)
		}
		_, _ = w.Write([]byte(trackerBody))
	}))
	defer trackerServer.Close()

	meta := &metainfo.MetaInfo{
		Announce: trackerServer.URL,
		InfoHash: sha1.Sum([]byte("test torrent")),
		Info: metainfo.Info{
			Name:        "payload.bin",
			Pieces:      hashes,
			PieceLength: pieceLength,
			Length:      int64(len(data)),
		},
	}
	peerError := make(chan error, 1)
	go func() { peerError <- servePeer(listener, meta, data) }()

	destination := make(bufferAt, len(data))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gotorrent.Download(ctx, meta, destination); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(destination, data) {
		t.Fatal("downloaded data does not match payload")
	}
	if err := <-peerError; err != nil {
		t.Fatalf("fake peer: %v", err)
	}
	if started.Load() != 1 || completed.Load() != 1 {
		t.Fatalf("tracker events: started=%d completed=%d", started.Load(), completed.Load())
	}
}

func TestDownloadRejectsInvalidInput(t *testing.T) {
	valid := &metainfo.MetaInfo{Info: metainfo.Info{
		PieceLength: 1,
		Length:      1,
		Pieces:      []metainfo.Hash{{}},
	}}
	if err := gotorrent.Download(context.Background(), valid, nil); err == nil {
		t.Fatal("Download accepted nil destination")
	}
	tests := []struct {
		name string
		meta *metainfo.MetaInfo
		dst  bufferAt
	}{
		{name: "nil metainfo", dst: make(bufferAt, 1)},
		{name: "invalid piece length", meta: &metainfo.MetaInfo{Info: metainfo.Info{Length: 1}}, dst: make(bufferAt, 1)},
		{name: "oversized piece", meta: &metainfo.MetaInfo{Info: metainfo.Info{
			PieceLength: 65 << 20,
			Length:      1,
			Pieces:      []metainfo.Hash{{}},
		}}, dst: make(bufferAt, 1)},
		{name: "negative length", meta: &metainfo.MetaInfo{Info: metainfo.Info{PieceLength: 1, Length: -1}}, dst: make(bufferAt, 1)},
		{name: "wrong piece count", meta: &metainfo.MetaInfo{Info: metainfo.Info{PieceLength: 1, Length: 1}}, dst: make(bufferAt, 1)},
		{name: "both file modes", meta: &metainfo.MetaInfo{Info: metainfo.Info{
			PieceLength: 1,
			Length:      1,
			Files:       []metainfo.FileInfo{{Path: []string{"file"}, Length: 1}},
		}}, dst: make(bufferAt, 1)},
		{name: "negative multi-file length", meta: &metainfo.MetaInfo{Info: metainfo.Info{
			PieceLength: 1,
			Files:       []metainfo.FileInfo{{Path: []string{"file"}, Length: -1}},
		}}, dst: make(bufferAt, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := gotorrent.Download(context.Background(), test.meta, test.dst); err == nil {
				t.Fatal("Download accepted invalid input")
			}
		})
	}
}

func TestValidate(t *testing.T) {
	meta := &metainfo.MetaInfo{Info: metainfo.Info{Length: 0, PieceLength: 1}}
	if err := gotorrent.Validate(meta); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := gotorrent.Validate(nil); err == nil {
		t.Fatal("Validate accepted nil metainfo")
	}
}

func TestDownloadNoPeers(t *testing.T) {
	body, err := bencode.EncodeToString(map[string]any{"interval": int64(60), "peers": ""})
	if err != nil {
		t.Fatal(err)
	}
	var stopped atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("event") == "stopped" {
			stopped.Add(1)
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	meta := &metainfo.MetaInfo{
		Announce: server.URL,
		Info: metainfo.Info{
			PieceLength: 1,
			Length:      1,
			Pieces:      []metainfo.Hash{{}},
		},
	}
	if err := gotorrent.Download(context.Background(), meta, make(bufferAt, 1)); err == nil {
		t.Fatal("Download succeeded without peers")
	}
	if stopped.Load() != 1 {
		t.Fatalf("stopped announces = %d, want 1", stopped.Load())
	}
}

func TestDownloadUnreachablePeer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portString, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()

	body, err := bencode.EncodeToString(map[string]any{
		"interval": int64(60),
		"peers":    string([]byte{127, 0, 0, 1, byte(port >> 8), byte(port)}),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	meta := &metainfo.MetaInfo{
		Announce: server.URL,
		Info: metainfo.Info{
			PieceLength: 1,
			Length:      1,
			Pieces:      []metainfo.Hash{{}},
		},
	}
	if err := gotorrent.Download(context.Background(), meta, make(bufferAt, 1)); err == nil {
		t.Fatal("Download succeeded with an unreachable peer")
	}
}

func TestDownloadCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	meta := &metainfo.MetaInfo{
		Announce: "http://tracker.invalid",
		Info: metainfo.Info{
			PieceLength: 1,
			Length:      1,
			Pieces:      []metainfo.Hash{{}},
		},
	}
	if err := gotorrent.Download(ctx, meta, make(bufferAt, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Download error = %v, want context cancellation", err)
	}
}

func servePeer(listener net.Listener, meta *metainfo.MetaInfo, data []byte) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if _, err := peer.Handshake(conn, meta.InfoHash, [20]byte{'s', 'e', 'e', 'd'}); err != nil {
		return err
	}
	message, err := peer.ReadMessage(conn)
	if err != nil {
		return err
	}
	if message == nil || message.ID != peer.MsgInterested {
		return fmt.Errorf("got message %v, want interested", message)
	}
	if err := binary.Write(conn, binary.BigEndian, uint32(0)); err != nil {
		return err
	}

	pieces := bitfield.New(len(meta.Info.Pieces))
	for index := range meta.Info.Pieces {
		pieces.Set(index)
	}
	if err := peer.WriteMessage(conn, &peer.Message{ID: peer.MsgBitfield, Payload: pieces}); err != nil {
		return err
	}
	have := make([]byte, 4)
	if err := peer.WriteMessage(conn, &peer.Message{ID: peer.MsgHave, Payload: have}); err != nil {
		return err
	}
	if err := peer.WriteMessage(conn, &peer.Message{ID: peer.MsgChoke}); err != nil {
		return err
	}
	if err := peer.WriteMessage(conn, &peer.Message{ID: peer.MsgUnchoke}); err != nil {
		return err
	}

	blocks := 0
	for index := range meta.Info.Pieces {
		length := min(meta.Info.PieceLength, int64(len(data))-int64(index)*meta.Info.PieceLength)
		blocks += int((length + piece.BlockSize - 1) / piece.BlockSize)
	}
	for range blocks {
		message, err := peer.ReadMessage(conn)
		if err != nil {
			return err
		}
		if message == nil || message.ID != peer.MsgRequest || len(message.Payload) != 12 {
			return fmt.Errorf("got message %v, want request", message)
		}
		index := binary.BigEndian.Uint32(message.Payload[0:4])
		begin := binary.BigEndian.Uint32(message.Payload[4:8])
		length := binary.BigEndian.Uint32(message.Payload[8:12])
		offset := int64(index)*meta.Info.PieceLength + int64(begin)
		end := offset + int64(length)
		if offset < 0 || end > int64(len(data)) {
			return fmt.Errorf("request [%d,%d) is out of range", offset, end)
		}
		payload := make([]byte, 8+length)
		binary.BigEndian.PutUint32(payload[0:4], index)
		binary.BigEndian.PutUint32(payload[4:8], begin)
		copy(payload[8:], data[offset:end])
		if err := peer.WriteMessage(conn, &peer.Message{ID: peer.MsgPiece, Payload: payload}); err != nil {
			return err
		}
	}
	return nil
}
