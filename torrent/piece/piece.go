// Package piece manages per-piece download state for a BitTorrent client.
//
// Each piece is divided into fixed-size blocks (see [BlockSize]). A [State]
// tracks which blocks have been requested and received, accumulates the
// downloaded data, and verifies the final SHA-1 hash against the value in
// the .torrent metainfo.
package piece

import (
	"crypto/sha1"
	"fmt"

	"github.com/natalie-o-perret/go-protocols/torrent/metainfo"
)

// BlockSize is the standard block request size (16 KiB) used in the peer wire
// protocol.
const BlockSize = 1 << 14 // 16384 bytes

// State tracks the download progress of a single piece.
type State struct {
	data       []byte
	blocks     []uint8
	index      int
	length     int
	downloaded int
	hash       metainfo.Hash
}

// New creates a State for the piece at index with the given expected hash and
// byte length.
func New(index int, hash metainfo.Hash, length int) *State {
	return &State{
		index:  index,
		hash:   hash,
		length: length,
		data:   make([]byte, length),
		blocks: make([]uint8, (length+BlockSize-1)/BlockSize),
	}
}

// Index returns the piece index.
func (s *State) Index() int { return s.index }

// Length returns the piece length in bytes.
func (s *State) Length() int { return s.length }

// Complete reports whether all bytes of the piece have been downloaded.
func (s *State) Complete() bool { return s.downloaded >= s.length }

// NextRequest returns the offset and block length of the next block to
// request, and whether there are still blocks left to request.
//
// Calling NextRequest marks the block as requested. Pairs with [State.Store]
// to fill the piece buffer and [State.ResetRequests] to retry dropped requests.
func (s *State) NextRequest() (begin, blockLen int, ok bool) {
	for i, status := range s.blocks {
		if status != 0 {
			continue
		}
		begin = i * BlockSize
		blockLen = min(BlockSize, s.length-begin)
		s.blocks[i] = 1
		return begin, blockLen, true
	}
	return 0, 0, false
}

// ResetRequests makes requested but unreceived blocks available for retry.
// Peers discard outstanding requests when they choke a client.
func (s *State) ResetRequests() {
	for i, status := range s.blocks {
		if status == 1 {
			s.blocks[i] = 0
		}
	}
}

// Store writes one complete, block-aligned response into the piece buffer.
func (s *State) Store(begin int, data []byte) error {
	if begin < 0 || begin >= s.length || begin%BlockSize != 0 {
		return fmt.Errorf("piece %d: invalid block offset %d", s.index, begin)
	}
	block := begin / BlockSize
	expected := min(BlockSize, s.length-begin)
	if len(data) != expected {
		return fmt.Errorf("piece %d: block at offset %d has len %d, want %d",
			s.index, begin, len(data), expected)
	}
	if s.blocks[block] == 2 {
		return nil
	}
	copy(s.data[begin:], data)
	s.downloaded += len(data)
	s.blocks[block] = 2
	return nil
}

// Verify checks the downloaded data against the expected SHA-1 hash.
// Call only after [State.Complete] returns true.
func (s *State) Verify() error {
	got := sha1.Sum(s.data)
	if got != s.hash {
		return fmt.Errorf("piece %d: hash mismatch: got %x, want %x", s.index, got, s.hash)
	}
	return nil
}

// Data returns the fully downloaded and verified piece data.
// Call only after [State.Complete] and [State.Verify] succeed.
func (s *State) Data() []byte {
	return s.data
}
