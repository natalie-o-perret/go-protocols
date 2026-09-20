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
	"strings"

	"github.com/natalie-o-perret/go-torrent/bencode"
)

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
	if info.Length > 0 {
		return info.Length
	}
	var total int64
	for _, f := range info.Files {
		total += f.Length
	}
	return total
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
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("metainfo: read: %w", err)
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

	if v, ok := d["name"]; ok {
		info.Name, _ = v.(string)
	}
	if v, ok := d["piece length"]; ok {
		if n, ok := v.(int64); ok {
			info.PieceLength = n
		}
	}
	if v, ok := d["length"]; ok {
		if n, ok := v.(int64); ok {
			info.Length = n
		}
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

	if v, ok := d["files"]; ok {
		fileList, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("metainfo: 'files' is not a list")
		}
		for _, f := range fileList {
			fd, ok := f.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("metainfo: file entry is not a dictionary")
			}
			fi := parseFileInfo(fd)
			if fi == nil {
				continue
			}
			info.Files = append(info.Files, *fi)
		}
	}

	return info, nil
}

func parseFileInfo(d map[string]any) *FileInfo {
	fi := &FileInfo{}
	if v, ok := d["length"]; ok {
		if n, ok := v.(int64); ok {
			fi.Length = n
		}
	}
	if v, ok := d["path"]; ok {
		if pathList, ok := v.([]any); ok {
			for _, p := range pathList {
				if s, ok := p.(string); ok {
					fi.Path = append(fi.Path, s)
				}
			}
		}
	}
	return fi
}
