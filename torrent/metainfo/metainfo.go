// Package metainfo parses .torrent files as specified by BEP 3.
//
// A .torrent file is a bencoded dictionary containing an "info"
// sub-dictionary whose SHA-1 hash is the InfoHash -- the unique identifier
// of the torrent in the BitTorrent network.
//
// [Decode] returns a [MetaInfo] with the Info dictionary, computed InfoHash,
// announce URL(s), and optional metadata such as comment and creation date.
package metainfo

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/natalie-o-perret/go-protocols/torrent/bencode"
)

const maxMetaInfoSize = 64 << 20

// Hash is a 20-byte SHA-1 digest.
type Hash [20]byte

// String returns the lowercase hex representation of h.
func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

// FileInfo describes a single file within a multi-file torrent.
type FileInfo struct {
	Path   []string
	Length int64
}

// Info is the "info" dictionary of a .torrent file.
type Info struct {
	Name        string
	Pieces      []Hash
	Files       []FileInfo
	PieceLength int64
	Length      int64
}

// TotalLength returns the total download size in bytes.
func (info *Info) TotalLength() int64 {
	if len(info.Files) == 0 {
		return info.Length
	}
	var total int64
	for _, f := range info.Files {
		total += f.Length
	}
	return total
}

// Validate checks the lengths and piece hashes in an info dictionary.
func (info *Info) Validate() error {
	if info.PieceLength <= 0 {
		return fmt.Errorf("metainfo: invalid piece length %d", info.PieceLength)
	}
	if info.Length < 0 {
		return fmt.Errorf("metainfo: invalid file length %d", info.Length)
	}
	if len(info.Files) > 0 && info.Length != 0 {
		return fmt.Errorf("metainfo: info contains both length and files")
	}

	var total int64
	if len(info.Files) == 0 {
		total = info.Length
	} else {
		for _, file := range info.Files {
			if file.Length < 0 || file.Length > math.MaxInt64-total {
				return fmt.Errorf("metainfo: invalid file length %d", file.Length)
			}
			if len(file.Path) == 0 {
				return fmt.Errorf("metainfo: file path is empty")
			}
			total += file.Length
		}
	}

	expected := int64(0)
	if total > 0 {
		expected = (total-1)/info.PieceLength + 1
	}
	if int64(len(info.Pieces)) != expected {
		return fmt.Errorf("metainfo: %d piece hashes, want %d for %d bytes", len(info.Pieces), expected, total)
	}
	return nil
}

// PieceCount returns the number of pieces.
func (info *Info) PieceCount() int {
	return len(info.Pieces)
}

// MetaInfo represents a parsed .torrent file.
type MetaInfo struct {
	Announce     string
	Comment      string
	CreatedBy    string
	AnnounceList [][]string
	Info         Info
	CreationDate int64
	InfoHash     Hash
}

// Trackers returns a deduplicated, ordered list of tracker URLs.
// The Announce URL (if non-empty) is always first, followed by AnnounceList
// entries in tier order.
func (m *MetaInfo) Trackers() []string {
	seen := make(map[string]struct{})
	var result []string
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" {
			return
		}
		if _, ok := seen[u]; ok {
			return
		}
		seen[u] = struct{}{}
		result = append(result, u)
	}
	add(m.Announce)
	for _, tier := range m.AnnounceList {
		for _, u := range tier {
			add(u)
		}
	}
	return result
}

// Decode parses a .torrent file from r and returns a [MetaInfo].
//
// The InfoHash is computed by bencoding the "info" dictionary with
// lexicographically sorted keys (required by BEP 3) and taking the SHA-1 of
// the result.
func Decode(r io.Reader) (*MetaInfo, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxMetaInfoSize+1))
	if err != nil {
		return nil, fmt.Errorf("metainfo: read: %w", err)
	}
	if len(data) > maxMetaInfoSize {
		return nil, fmt.Errorf("metainfo: file exceeds %d bytes", maxMetaInfoSize)
	}
	raw, err := bencode.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("metainfo: decode bencode: %w", err)
	}
	dict, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("metainfo: top-level value is not a dictionary")
	}

	infoRaw, ok := dict["info"]
	if !ok {
		return nil, fmt.Errorf("metainfo: missing 'info' key")
	}
	infoDict, ok := infoRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("metainfo: 'info' is not a dictionary")
	}

	// Compute InfoHash from the re-bencoded info dictionary.
	// BEP 3 requires dict keys to be sorted, so re-encoding with sorted keys
	// produces a canonical form that matches the original for any
	// spec-compliant .torrent file.
	infoEncoded, err := bencode.EncodeToString(infoRaw)
	if err != nil {
		return nil, fmt.Errorf("metainfo: re-encode info dict: %w", err)
	}

	m := &MetaInfo{}
	m.InfoHash = sha1.Sum([]byte(infoEncoded))

	info, err := parseInfo(infoDict)
	if err != nil {
		return nil, err
	}
	m.Info = *info

	if v, ok := dict["announce"]; ok {
		m.Announce, _ = v.(string)
	}
	if v, ok := dict["comment"]; ok {
		m.Comment, _ = v.(string)
	}
	if v, ok := dict["created by"]; ok {
		m.CreatedBy, _ = v.(string)
	}
	if v, ok := dict["creation date"]; ok {
		if n, ok := v.(int64); ok {
			m.CreationDate = n
		}
	}
	if v, ok := dict["announce-list"]; ok {
		if tiers, ok := v.([]any); ok {
			for _, tier := range tiers {
				if trackers, ok := tier.([]any); ok {
					var tierList []string
					for _, t := range trackers {
						if s, ok := t.(string); ok {
							tierList = append(tierList, s)
						}
					}
					m.AnnounceList = append(m.AnnounceList, tierList)
				}
			}
		}
	}

	return m, nil
}

func parseInfo(d map[string]any) (*Info, error) {
	info := &Info{}

	name, ok := d["name"].(string)
	if !ok || name == "" {
		return nil, fmt.Errorf("metainfo: missing or invalid 'name' key")
	}
	info.Name = name
	pieceLength, ok := d["piece length"].(int64)
	if !ok {
		return nil, fmt.Errorf("metainfo: missing or invalid 'piece length' key")
	}
	info.PieceLength = pieceLength

	lengthRaw, hasLength := d["length"]
	filesRaw, hasFiles := d["files"]
	if hasLength == hasFiles {
		return nil, fmt.Errorf("metainfo: info must contain exactly one of 'length' or 'files'")
	}
	if hasLength {
		length, ok := lengthRaw.(int64)
		if !ok {
			return nil, fmt.Errorf("metainfo: 'length' is not an integer")
		}
		info.Length = length
	}

	piecesRaw, ok := d["pieces"]
	if !ok {
		return nil, fmt.Errorf("metainfo: missing 'pieces' key")
	}
	piecesStr, ok := piecesRaw.(string)
	if !ok {
		return nil, fmt.Errorf("metainfo: 'pieces' is not a string")
	}
	if len(piecesStr)%20 != 0 {
		return nil, fmt.Errorf("metainfo: 'pieces' length %d is not a multiple of 20", len(piecesStr))
	}
	info.Pieces = make([]Hash, len(piecesStr)/20)
	for i := range info.Pieces {
		copy(info.Pieces[i][:], piecesStr[i*20:(i+1)*20])
	}

	if hasFiles {
		fileList, ok := filesRaw.([]any)
		if !ok {
			return nil, fmt.Errorf("metainfo: 'files' is not a list")
		}
		if len(fileList) == 0 {
			return nil, fmt.Errorf("metainfo: 'files' is empty")
		}
		for _, f := range fileList {
			fd, ok := f.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("metainfo: file entry is not a dictionary")
			}
			fi, err := parseFileInfo(fd)
			if err != nil {
				return nil, err
			}
			info.Files = append(info.Files, *fi)
		}
	}

	if err := info.Validate(); err != nil {
		return nil, err
	}
	return info, nil
}

func parseFileInfo(d map[string]any) (*FileInfo, error) {
	length, ok := d["length"].(int64)
	if !ok {
		return nil, fmt.Errorf("metainfo: file 'length' is not an integer")
	}
	pathList, ok := d["path"].([]any)
	if !ok || len(pathList) == 0 {
		return nil, fmt.Errorf("metainfo: file 'path' is not a non-empty list")
	}
	fi := &FileInfo{Length: length}
	for _, part := range pathList {
		value, ok := part.(string)
		if !ok || value == "" {
			return nil, fmt.Errorf("metainfo: file path component is not a non-empty string")
		}
		fi.Path = append(fi.Path, value)
	}
	return fi, nil
}
