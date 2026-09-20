package peer_test

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"

	"github.com/natalie-o-perret/go-torrent/metainfo"
	"github.com/natalie-o-perret/go-torrent/peer"
)

func TestHandshake(t *testing.T) {
	infoHash := metainfo.Hash{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	peerIDA := [20]byte{'A'}
	peerIDB := [20]byte{'B'}

	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()

	errs := make(chan error, 2)
	results := make(chan [20]byte, 2)

	go func() {
		id, err := peer.Handshake(clientConn, infoHash, peerIDA)
		errs <- err
		results <- id
	}()
	go func() {
		id, err := peer.Handshake(serverConn, infoHash, peerIDB)
		errs <- err
		results <- id
	}()

	for range 2 {
		if err := <-errs; err != nil {
			t.Errorf("Handshake: %v", err)
		}
	}

	ids := [2][20]byte{<-results, <-results}
	found := func(want [20]byte) bool {
		for _, id := range ids {
			if id == want {
				return true
			}
		}
		return false
	}
	if !found(peerIDA) {
		t.Error("peerIDA not received in handshake")
	}
	if !found(peerIDB) {
		t.Error("peerIDB not received in handshake")
	}
}

func TestHandshakeInfoHashMismatch(t *testing.T) {
	infoHashA := metainfo.Hash{1}
	infoHashB := metainfo.Hash{2}
	peerID := [20]byte{}

	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()

	errs := make(chan error, 2)
	go func() { _, err := peer.Handshake(clientConn, infoHashA, peerID); errs <- err }()
	go func() { _, err := peer.Handshake(serverConn, infoHashB, peerID); errs <- err }()

	errA := <-errs
	errB := <-errs
	if errA == nil && errB == nil {
		t.Error("want at least one error for info hash mismatch, got nil on both sides")
	}
}

func TestReadWriteMessage(t *testing.T) {
	tests := []struct {
		msg  *peer.Message
		name string
	}{
		{name: "choke", msg: &peer.Message{ID: peer.MsgChoke, Payload: nil}},
		{name: "unchoke", msg: &peer.Message{ID: peer.MsgUnchoke}},
		{name: "interested", msg: &peer.Message{ID: peer.MsgInterested}},
		{name: "bitfield", msg: &peer.Message{ID: peer.MsgBitfield, Payload: []byte{0xff, 0x80}}},
		{name: "have", msg: &peer.Message{ID: peer.MsgHave, Payload: []byte{0, 0, 0, 5}}},
	}
	for _, tc := range tests {
		var buf bytes.Buffer
		if err := peer.WriteMessage(&buf, tc.msg); err != nil {
			t.Errorf("%s: WriteMessage: %v", tc.name, err)
			continue
		}
		got, err := peer.ReadMessage(&buf)
		if err != nil {
			t.Errorf("%s: ReadMessage: %v", tc.name, err)
			continue
		}
		if got == nil {
			t.Errorf("%s: ReadMessage returned nil", tc.name)
			continue
		}
		if got.ID != tc.msg.ID {
			t.Errorf("%s: ID = %d, want %d", tc.name, got.ID, tc.msg.ID)
		}
		if !bytes.Equal(got.Payload, tc.msg.Payload) {
			t.Errorf("%s: Payload = %v, want %v", tc.name, got.Payload, tc.msg.Payload)
		}
	}
}

func TestReadMessageKeepalive(t *testing.T) {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint32(0))
	msg, err := peer.ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage keepalive: %v", err)
	}
	if msg != nil {
		t.Errorf("keepalive: got %+v, want nil", msg)
	}
}

func TestFormatRequest(t *testing.T) {
	payload := peer.FormatRequest(5, 16384, 16384)
	if len(payload) != 12 {
		t.Fatalf("FormatRequest len = %d, want 12", len(payload))
	}
	index := binary.BigEndian.Uint32(payload[0:4])
	begin := binary.BigEndian.Uint32(payload[4:8])
	length := binary.BigEndian.Uint32(payload[8:12])
	if index != 5 || begin != 16384 || length != 16384 {
		t.Errorf("FormatRequest = (%d, %d, %d), want (5, 16384, 16384)", index, begin, length)
	}
}

func TestParsePiece(t *testing.T) {
	payload := make([]byte, 12)
	binary.BigEndian.PutUint32(payload[0:4], 3)
	binary.BigEndian.PutUint32(payload[4:8], 32768)
	copy(payload[8:], []byte{1, 2, 3, 4})

	index, begin, data, err := peer.ParsePiece(payload)
	if err != nil {
		t.Fatalf("ParsePiece: %v", err)
	}
	if index != 3 {
		t.Errorf("index = %d, want 3", index)
	}
	if begin != 32768 {
		t.Errorf("begin = %d, want 32768", begin)
	}
	if !bytes.Equal(data, []byte{1, 2, 3, 4}) {
		t.Errorf("data = %v", data)
	}
}

func TestParsePieceTooShort(t *testing.T) {
	_, _, _, err := peer.ParsePiece([]byte{0, 1, 2})
	if err == nil {
		t.Error("want error for payload < 8 bytes, got nil")
	}
}

func TestParseHave(t *testing.T) {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, 42)

	index, err := peer.ParseHave(payload)
	if err != nil {
		t.Fatalf("ParseHave: %v", err)
	}
	if index != 42 {
		t.Errorf("index = %d, want 42", index)
	}
}

func TestParseHaveWrongLength(t *testing.T) {
	_, err := peer.ParseHave([]byte{0, 0, 0})
	if err == nil {
		t.Error("want error for have payload != 4 bytes, got nil")
	}
}
