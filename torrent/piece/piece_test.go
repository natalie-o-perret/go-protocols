package piece_test

import (
	"bytes"
	"crypto/sha1"
	"testing"

	"github.com/natalie-o-perret/go-torrent/metainfo"
	"github.com/natalie-o-perret/go-torrent/piece"
)

func makeHash(data []byte) metainfo.Hash {
	return sha1.Sum(data)
}

func TestNewState(t *testing.T) {
	h := metainfo.Hash{}
	s := piece.New(0, h, 100)
	if s.Index() != 0 {
		t.Errorf("Index = %d, want 0", s.Index())
	}
	if s.Length() != 100 {
		t.Errorf("Length = %d, want 100", s.Length())
	}
	if s.Complete() {
		t.Error("Complete() = true on fresh state, want false")
	}
}

func TestNextRequest(t *testing.T) {
	s := piece.New(0, metainfo.Hash{}, 100)
	begin, blockLen, ok := s.NextRequest()
	if !ok {
		t.Fatal("NextRequest: ok = false on first call")
	}
	if begin != 0 {
		t.Errorf("begin = %d, want 0", begin)
	}
	if blockLen != 100 {
		t.Errorf("blockLen = %d, want 100", blockLen)
	}
	_, _, ok = s.NextRequest()
	if ok {
		t.Error("NextRequest: ok = true after all blocks requested")
	}
}

func TestNextRequestMultipleBlocks(t *testing.T) {
	total := piece.BlockSize*3 + 500
	s := piece.New(1, metainfo.Hash{}, total)

	expected := []struct {
		begin    int
		blockLen int
	}{
		{0, piece.BlockSize},
		{piece.BlockSize, piece.BlockSize},
		{piece.BlockSize * 2, piece.BlockSize},
		{piece.BlockSize * 3, 500},
	}

	for i, want := range expected {
		begin, blockLen, ok := s.NextRequest()
		if !ok {
			t.Fatalf("call %d: ok = false", i)
		}
		if begin != want.begin || blockLen != want.blockLen {
			t.Errorf("call %d: got (%d, %d), want (%d, %d)", i, begin, blockLen, want.begin, want.blockLen)
		}
	}
	_, _, ok := s.NextRequest()
	if ok {
		t.Error("NextRequest returned ok=true after all blocks requested")
	}
}

func TestStoreAndComplete(t *testing.T) {
	data := []byte("hello, torrent!")
	hash := makeHash(data)
	s := piece.New(0, hash, len(data))

	if err := s.Store(0, data); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if !s.Complete() {
		t.Error("Complete() = false after full store")
	}
}

func TestStoreOverflow(t *testing.T) {
	s := piece.New(0, metainfo.Hash{}, 10)
	err := s.Store(8, []byte{1, 2, 3})
	if err == nil {
		t.Error("want error for overflow Store, got nil")
	}
}

func TestVerify(t *testing.T) {
	data := []byte("some piece data here, four score and seven bytes ago")
	hash := makeHash(data)
	s := piece.New(2, hash, len(data))
	if err := s.Store(0, data); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if err := s.Verify(); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestVerifyMismatch(t *testing.T) {
	data := []byte("correct data")
	wrongHash := metainfo.Hash{0xde, 0xad}
	s := piece.New(0, wrongHash, len(data))
	if err := s.Store(0, data); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if err := s.Verify(); err == nil {
		t.Error("Verify: want error for hash mismatch, got nil")
	}
}

func TestData(t *testing.T) {
	data := []byte("piece payload")
	hash := makeHash(data)
	s := piece.New(0, hash, len(data))
	if err := s.Store(0, data); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	got := s.Data()
	if !bytes.Equal(got, data) {
		t.Errorf("Data() = %q, want %q", got, data)
	}
}

func TestBlockSize(t *testing.T) {
	if piece.BlockSize != 16384 {
		t.Errorf("BlockSize = %d, want 16384", piece.BlockSize)
	}
}
