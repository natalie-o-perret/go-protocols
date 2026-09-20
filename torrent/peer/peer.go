// Package peer implements the BitTorrent peer wire protocol as defined in BEP 3.
//
// It covers the initial handshake and all standard message types: Choke,
// Unchoke, Interested, NotInterested, Have, Bitfield, Request, Piece, and
// Cancel.
//
// Typical usage:
//
// conn, _ := net.DialTimeout("tcp", addr, 10*time.Second)
// remoteID, err := peer.Handshake(conn, infoHash, myPeerID)
// msg, err := peer.ReadMessage(conn)
package peer

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/natalie-o-perret/go-torrent/metainfo"
)

const (
	protocolStr    = "BitTorrent protocol"
	protocolStrLen = 19
	// handshakeLen is the total byte length of a BEP 3 handshake:
	// 1 (pstrlen) + 19 (pstr) + 8 (reserved) + 20 (info_hash) + 20 (peer_id)
	handshakeLen     = 68
	handshakeTimeout = 3 * time.Second
)

// MessageID identifies a peer wire protocol message type.
type MessageID uint8

const (
	// MsgChoke is sent to tell the remote peer it is choked.
	MsgChoke MessageID = 0
	// MsgUnchoke is sent to tell the remote peer it is unchoked.
	MsgUnchoke MessageID = 1
	// MsgInterested indicates local interest in the remote peer's pieces.
	MsgInterested MessageID = 2
	// MsgNotInterested indicates no local interest in the remote peer's pieces.
	MsgNotInterested MessageID = 3
	// MsgHave announces that the local peer has successfully downloaded a piece.
	MsgHave MessageID = 4
	// MsgBitfield carries the set of pieces the sender has.
	MsgBitfield MessageID = 5
	// MsgRequest asks the remote peer for a block of data.
	MsgRequest MessageID = 6
	// MsgPiece carries a block of piece data.
	MsgPiece MessageID = 7
	// MsgCancel cancels a previously sent Request.
	MsgCancel MessageID = 8
)

// Message is a peer wire protocol message.
type Message struct {
	Payload []byte
	ID      MessageID
}

// Handshake performs the BEP 3 handshake over conn.
//
// It sends our handshake then reads and validates the remote handshake.
// The connection deadline is set to handshakeTimeout for the duration of
// the exchange. Returns the remote peer ID on success.
func Handshake(conn net.Conn, infoHash metainfo.Hash, peerID [20]byte) ([20]byte, error) {
	if err := conn.SetDeadline(time.Now().Add(handshakeTimeout)); err != nil {
		return [20]byte{}, fmt.Errorf("peer: set handshake deadline: %w", err)
	}
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	// Send and receive concurrently — net.Pipe and similar connections have no
	// internal buffer, so send-then-receive would deadlock when both sides
	// initiate the handshake at the same time.
	sendErrCh := make(chan error, 1)
	go func() { sendErrCh <- sendHandshake(conn, infoHash, peerID) }()

	remotePeerID, recvErr := recvHandshake(conn, infoHash)
	if se := <-sendErrCh; se != nil {
		return [20]byte{}, fmt.Errorf("peer: send handshake: %w", se)
	}
	if recvErr != nil {
		return [20]byte{}, recvErr
	}
	return remotePeerID, nil
}

func sendHandshake(w io.Writer, infoHash metainfo.Hash, peerID [20]byte) error {
	buf := make([]byte, handshakeLen)
	buf[0] = protocolStrLen
	copy(buf[1:20], protocolStr)
	// buf[20:28] -- eight reserved bytes, left as zero
	copy(buf[28:48], infoHash[:])
	copy(buf[48:68], peerID[:])
	_, err := w.Write(buf)
	return err
}

func recvHandshake(r io.Reader, expectedHash metainfo.Hash) ([20]byte, error) {
	buf := make([]byte, handshakeLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return [20]byte{}, fmt.Errorf("peer: read handshake: %w", err)
	}
	if buf[0] != protocolStrLen {
		return [20]byte{}, fmt.Errorf("peer: unexpected protocol name length %d", buf[0])
	}
	if string(buf[1:20]) != protocolStr {
		return [20]byte{}, fmt.Errorf("peer: unexpected protocol %q", string(buf[1:20]))
	}
	var gotHash metainfo.Hash
	copy(gotHash[:], buf[28:48])
	if gotHash != expectedHash {
		return [20]byte{}, fmt.Errorf("peer: info hash mismatch: got %s, want %s", gotHash, expectedHash)
	}
	var remotePeerID [20]byte
	copy(remotePeerID[:], buf[48:68])
	return remotePeerID, nil
}

// ReadMessage reads the next length-prefixed message from r.
// Returns nil for keepalive messages (zero-length prefix).
func ReadMessage(r io.Reader) (*Message, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, fmt.Errorf("peer: read message length: %w", err)
	}
	if length == 0 {
		return nil, nil // keepalive
	}
	raw := make([]byte, length)
	if _, err := io.ReadFull(r, raw); err != nil {
		return nil, fmt.Errorf("peer: read message payload: %w", err)
	}
	return &Message{ID: MessageID(raw[0]), Payload: raw[1:]}, nil
}

// WriteMessage writes a length-prefixed message to w.
func WriteMessage(w io.Writer, msg *Message) error {
	length := uint32(1 + len(msg.Payload))
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return fmt.Errorf("peer: write message length: %w", err)
	}
	if _, err := w.Write([]byte{byte(msg.ID)}); err != nil {
		return fmt.Errorf("peer: write message id: %w", err)
	}
	if _, err := w.Write(msg.Payload); err != nil {
		return fmt.Errorf("peer: write message payload: %w", err)
	}
	return nil
}

// FormatRequest builds the 12-byte payload for a Request message.
func FormatRequest(index, begin, length uint32) []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[0:4], index)
	binary.BigEndian.PutUint32(buf[4:8], begin)
	binary.BigEndian.PutUint32(buf[8:12], length)
	return buf
}

// ParsePiece parses the payload of a Piece message, returning the piece index,
// block offset within the piece, and block data.
func ParsePiece(payload []byte) (index, begin uint32, data []byte, err error) {
	if len(payload) < 8 {
		return 0, 0, nil, fmt.Errorf("peer: piece payload too short: %d bytes", len(payload))
	}
	index = binary.BigEndian.Uint32(payload[0:4])
	begin = binary.BigEndian.Uint32(payload[4:8])
	data = payload[8:]
	return index, begin, data, nil
}

// ParseHave parses the payload of a Have message, returning the piece index.
func ParseHave(payload []byte) (uint32, error) {
	if len(payload) != 4 {
		return 0, fmt.Errorf("peer: have payload must be 4 bytes, got %d", len(payload))
	}
	return binary.BigEndian.Uint32(payload), nil
}
