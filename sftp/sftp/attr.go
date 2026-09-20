package sftp

import (
	"encoding/binary"
	"errors"
	"time"
)

// FileAttr mirrors the SSH_FXP_ATTRS structure. Fields with no
// applicable value use the SSH_FILEXFER_ATTR_* sentinel constants.
type FileAttr struct {
	Size  uint64
	UID   uint32
	GID   uint32
	Mode  uint32
	Mtime time.Time
	Atime time.Time
}

// AttributeFlag bits follow draft-ietf-secsh-filexfer-13 § 6.4.
const (
	attrSize   uint32 = 0x00000001
	attrUIDGID uint32 = 0x00000002
	attrMode   uint32 = 0x00000004
	attrMtime  uint32 = 0x00000008
	attrAtime  uint32 = 0x00000010
)

// Encode serializes a FileAttr into the SSH_FXP_ATTRS wire form.
func (a FileAttr) Encode() []byte {
	var flags uint32
	buf := make([]byte, 0, 32)
	if a.Size != 0 {
		flags |= attrSize
	}
	if a.UID != 0 || a.GID != 0 {
		flags |= attrUIDGID
	}
	if a.Mode != 0 {
		flags |= attrMode
	}
	if !a.Mtime.IsZero() {
		flags |= attrMtime
	}
	if !a.Atime.IsZero() {
		flags |= attrAtime
	}
	buf = AppendUint32(buf, flags)
	if flags&attrSize != 0 {
		buf = AppendUint64(buf, a.Size)
	}
	if flags&attrUIDGID != 0 {
		buf = AppendUint32(buf, a.UID)
		buf = AppendUint32(buf, a.GID)
	}
	if flags&attrMode != 0 {
		buf = AppendUint32(buf, a.Mode)
	}
	if flags&attrMtime != 0 {
		buf = AppendUint32(buf, uint32(a.Mtime.Unix()))
	}
	if flags&attrAtime != 0 {
		buf = AppendUint32(buf, uint32(a.Atime.Unix()))
	}
	return buf
}

// DecodeAttrs parses a SSH_FXP_ATTRS payload at offset off.
func DecodeAttrs(buf []byte, off int) (FileAttr, int, error) {
	flags, next, err := ReadUint32(buf, off)
	if err != nil {
		return FileAttr{}, 0, err
	}
	var a FileAttr
	if flags&attrSize != 0 {
		a.Size, next, err = ReadUint64(buf, next)
		if err != nil {
			return FileAttr{}, 0, err
		}
	}
	if flags&attrUIDGID != 0 {
		a.UID, next, err = ReadUint32(buf, next)
		if err != nil {
			return FileAttr{}, 0, err
		}
		a.GID, next, err = ReadUint32(buf, next)
		if err != nil {
			return FileAttr{}, 0, err
		}
	}
	if flags&attrMode != 0 {
		a.Mode, next, err = ReadUint32(buf, next)
		if err != nil {
			return FileAttr{}, 0, err
		}
	}
	if flags&attrMtime != 0 {
		var t uint32
		t, next, err = ReadUint32(buf, next)
		if err != nil {
			return FileAttr{}, 0, err
		}
		a.Mtime = time.Unix(int64(t), 0)
	}
	if flags&attrAtime != 0 {
		var t uint32
		t, next, err = ReadUint32(buf, next)
		if err != nil {
			return FileAttr{}, 0, err
		}
		a.Atime = time.Unix(int64(t), 0)
	}
	return a, next, nil
}

// EncodeStatus builds a STATUS packet payload. The caller supplies
// the request id, the status code, an error message and language.
func EncodeStatus(id RequestID, code StatusCode, msg, lang string) []byte {
	buf := make([]byte, 0, 16)
	buf = AppendUint32(buf, uint32(id))
	buf = AppendUint32(buf, uint32(code))
	buf = AppendString(buf, msg)
	buf = AppendString(buf, lang)
	return buf
}

// DecodeStatus parses a STATUS payload and returns the contained
// code and message.
func DecodeStatus(buf []byte) (id RequestID, code StatusCode, msg string, err error) {
	if len(buf) < 8 {
		return 0, 0, "", errors.New("sftp: status: short buffer")
	}
	id = RequestID(binary.BigEndian.Uint32(buf[:4]))
	code = StatusCode(binary.BigEndian.Uint32(buf[4:8]))
	msg, _, err = ReadString(buf, 8)
	if err != nil {
		return 0, 0, "", err
	}
	return id, code, msg, nil
}
