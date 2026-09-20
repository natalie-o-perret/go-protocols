package sftp

import (
	"bytes"
	"testing"
)

func TestPacketRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte("hello")
	if err := WritePacket(&buf, TypeVersion, payload); err != nil {
		t.Fatal(err)
	}
	pkt, err := ReadPacket(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if pkt.Type != TypeVersion {
		t.Fatalf("got type %d", pkt.Type)
	}
	if !bytes.Equal(pkt.Payload, payload) {
		t.Fatalf("got payload %q", pkt.Payload)
	}
}

func TestAppendReadString(t *testing.T) {
	buf := AppendString(nil, "abc")
	s, off, err := ReadString(buf, 0)
	if err != nil {
		t.Fatal(err)
	}
	if s != "abc" {
		t.Fatalf("got %q", s)
	}
	if off != 7 {
		t.Fatalf("off %d", off)
	}
}

func TestAppendReadUint32(t *testing.T) {
	buf := AppendUint32(nil, 0xdeadbeef)
	v, _, err := ReadUint32(buf, 0)
	if err != nil {
		t.Fatal(err)
	}
	if v != 0xdeadbeef {
		t.Fatalf("got %x", v)
	}
}

func TestFileAttrRoundtrip(t *testing.T) {
	a := FileAttr{Size: 1024, UID: 1000, GID: 1000, Mode: 0o644}
	enc := a.Encode()
	got, _, err := DecodeAttrs(enc, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Size != a.Size || got.UID != a.UID || got.GID != a.GID || got.Mode != a.Mode {
		t.Fatalf("got %+v want %+v", got, a)
	}
}
