package bitfield_test

import (
	"testing"

	"github.com/natalie-o-perret/go-torrent/bitfield"
)

func TestNew(t *testing.T) {
	tests := []struct {
		pieces  int
		wantLen int
	}{
		{0, 0},
		{1, 1},
		{8, 1},
		{9, 2},
		{16, 2},
		{17, 3},
		{100, 13},
	}
	for _, tc := range tests {
		b := bitfield.New(tc.pieces)
		if len(b) != tc.wantLen {
			t.Errorf("New(%d): len = %d, want %d", tc.pieces, len(b), tc.wantLen)
		}
	}
}

func TestHasAndSet(t *testing.T) {
	b := bitfield.New(16)

	for i := range 16 {
		if b.Has(i) {
			t.Errorf("Has(%d) = true before any Set", i)
		}
	}

	b.Set(0)
	if !b.Has(0) {
		t.Error("Has(0) = false after Set(0)")
	}
	if b[0] != 0x80 {
		t.Errorf("byte[0] = 0x%02x, want 0x80", b[0])
	}

	b.Set(7)
	if !b.Has(7) {
		t.Error("Has(7) = false after Set(7)")
	}
	if b[0] != 0x81 {
		t.Errorf("byte[0] = 0x%02x, want 0x81", b[0])
	}

	b.Set(8)
	if !b.Has(8) {
		t.Error("Has(8) = false after Set(8)")
	}
	if b[1] != 0x80 {
		t.Errorf("byte[1] = 0x%02x, want 0x80", b[1])
	}

	for _, i := range []int{1, 2, 3, 4, 5, 6, 9, 10, 11, 12, 13, 14, 15} {
		if b.Has(i) {
			t.Errorf("Has(%d) = true, expected false", i)
		}
	}
}

func TestHasOutOfBounds(t *testing.T) {
	b := bitfield.New(8)
	if b.Has(-1) {
		t.Error("Has(-1) should be false")
	}
	if b.Has(8) {
		t.Error("Has(8) should be false for 8-piece bitfield")
	}
}

func TestSetOutOfBounds(t *testing.T) {
	b := bitfield.New(8)
	b.Set(-1)
	b.Set(8)
	if b.Count() != 0 {
		t.Errorf("Count = %d after out-of-bounds Set, want 0", b.Count())
	}
}

func TestCount(t *testing.T) {
	b := bitfield.New(8)
	if b.Count() != 0 {
		t.Errorf("Count() = %d on empty bitfield, want 0", b.Count())
	}
	b.Set(0)
	b.Set(3)
	b.Set(7)
	if b.Count() != 3 {
		t.Errorf("Count() = %d, want 3", b.Count())
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		b       bitfield.Bitfield
		n       int
		wantErr bool
	}{
		{"exact fit", bitfield.New(8), 8, false},
		{"no spare bits", bitfield.New(16), 16, false},
		{"wrong length", bitfield.New(8), 9, true},
		{"spare bits set", bitfield.Bitfield{0x80, 0x01}, 9, true},
		{"spare bits clear", bitfield.Bitfield{0x80, 0x00}, 9, false},
	}
	for _, tc := range tests {
		err := tc.b.Validate(tc.n)
		if tc.wantErr && err == nil {
			t.Errorf("%s: want error, got nil", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
		}
	}
}
