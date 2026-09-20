package sftp

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Packet is a single SFTP message: a 4-byte length prefix, a 1-byte
// type, and a type-specific payload.
type Packet struct {
	Type    Type
	Payload []byte
}

// ReadPacket reads a single packet from r. The caller owns the
// returned slice and may reuse it.
func ReadPacket(r io.Reader) (Packet, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return Packet{}, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || n > 1<<24 {
		return Packet{}, fmt.Errorf("sftp: unreasonable packet length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return Packet{}, err
	}
	if len(buf) == 0 {
		return Packet{}, fmt.Errorf("sftp: zero-length payload")
	}
	return Packet{Type: Type(buf[0]), Payload: buf[1:]}, nil
}

// WritePacket writes a single packet to w. The 1-byte type prefix is
// prepended to payload before length-prefixing.
func WritePacket(w io.Writer, t Type, payload []byte) error {
	full := make([]byte, 1+len(payload))
	full[0] = byte(t)
	copy(full[1:], payload)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(full)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(full)
	return err
}

// ReadString reads a length-prefixed string from buf at offset off.
// The returned slice aliases the input.
func ReadString(buf []byte, off int) (s string, next int, err error) {
	if off+4 > len(buf) {
		return "", 0, fmt.Errorf("sftp: read string: short buffer")
	}
	n := binary.BigEndian.Uint32(buf[off : off+4])
	off += 4
	if off+int(n) > len(buf) {
		return "", 0, fmt.Errorf("sftp: read string: short buffer")
	}
	return string(buf[off : off+int(n)]), off + int(n), nil
}

// AppendString encodes s as a length-prefixed string and appends
// it to buf.
func AppendString(buf []byte, s string) []byte {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(s)))
	buf = append(buf, lenBuf[:]...)
	buf = append(buf, s...)
	return buf
}

// ReadUint32 reads a big-endian uint32 at off.
func ReadUint32(buf []byte, off int) (uint32, int, error) {
	if off+4 > len(buf) {
		return 0, 0, fmt.Errorf("sftp: read u32: short buffer")
	}
	return binary.BigEndian.Uint32(buf[off : off+4]), off + 4, nil
}

// AppendUint32 appends v as a big-endian uint32 to buf.
func AppendUint32(buf []byte, v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return append(buf, b[:]...)
}

// ReadUint64 reads a big-endian uint64 at off.
func ReadUint64(buf []byte, off int) (uint64, int, error) {
	if off+8 > len(buf) {
		return 0, 0, fmt.Errorf("sftp: read u64: short buffer")
	}
	return binary.BigEndian.Uint64(buf[off : off+8]), off + 8, nil
}

// AppendUint64 appends v as a big-endian uint64 to buf.
func AppendUint64(buf []byte, v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return append(buf, b[:]...)
}
