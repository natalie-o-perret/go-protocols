// Package bitfield provides a compact byte-slice bitfield for tracking which
// pieces a peer has downloaded in a BitTorrent swarm.
//
// The bitfield is stored as a []byte in network order: the most-significant bit
// of the first byte represents piece 0, matching the Bitfield message format
// defined in BEP 3.
package bitfield

import "fmt"

// Bitfield tracks which pieces a peer has.
// The underlying []byte is in network order (MSB of byte 0 = piece 0).
type Bitfield []byte

// New creates a Bitfield large enough to hold n pieces, with all bits cleared.
func New(n int) Bitfield {
	return make(Bitfield, (n+7)/8)
}

// Has reports whether piece i has been received.
func (b Bitfield) Has(i int) bool {
	if i < 0 || i >= len(b)*8 {
		return false
	}
	return b[i/8]>>uint(7-i%8)&1 == 1
}

// Set marks piece i as received.
func (b Bitfield) Set(i int) {
	if i < 0 || i >= len(b)*8 {
		return
	}
	b[i/8] |= 1 << uint(7-i%8)
}

// Count returns the number of pieces marked as received.
func (b Bitfield) Count() int {
	n := 0
	for _, octet := range b {
		for ; octet != 0; octet &= octet - 1 {
			n++
		}
	}
	return n
}

// Validate checks that b has the correct number of bytes for n pieces.
// Spare bits in the last byte must be zero per BEP 3.
func (b Bitfield) Validate(n int) error {
	expected := (n + 7) / 8
	if len(b) != expected {
		return fmt.Errorf("bitfield: length %d, want %d for %d pieces", len(b), expected, n)
	}
	if spare := n % 8; spare != 0 && len(b) > 0 {
		mask := byte(0xff >> spare)
		if b[len(b)-1]&mask != 0 {
			return fmt.Errorf("bitfield: spare bits in last byte are non-zero")
		}
	}
	return nil
}
