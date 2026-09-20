// Package bencode implements bencoding -- the binary serialisation format
// used by the BitTorrent protocol for .torrent files and tracker responses.
//
// The format supports four value types:
//   - integers: i<decimal>e  (e.g. "i42e")
//   - byte strings: <length>:<data>  (e.g. "4:spam")
//   - lists: l<items>e
//   - dictionaries: d<key><value>...e  (keys must be sorted lexicographically)
//
// [Decode] returns one of int64, string, []any, or map[string]any.
// [Encode] accepts the same set of types and additionally []byte.
package bencode

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Decode reads a bencoded value from r and returns it as one of:
// int64, string, []any, or map[string]any.
func Decode(r io.Reader) (any, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return decode(br)
}

func decode(r *bufio.Reader) (any, error) {
	b, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	switch {
	case b == 'i':
		return decodeInt(r)
	case b == 'l':
		return decodeList(r)
	case b == 'd':
		return decodeDict(r)
	case b >= '0' && b <= '9':
		_ = r.UnreadByte()
		return decodeString(r)
	default:
		return nil, fmt.Errorf("bencode: unexpected byte %q", b)
	}
}

// decodeInt parses an integer after the leading 'i' has been consumed.
func decodeInt(r *bufio.Reader) (int64, error) {
	s, err := r.ReadString('e')
	if err != nil {
		return 0, fmt.Errorf("bencode: read integer: %w", err)
	}
	s = s[:len(s)-1] // trim trailing 'e'
	if s == "-0" {
		return 0, errors.New("bencode: negative zero is invalid")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bencode: invalid integer %q: %w", s, err)
	}
	return n, nil
}

// decodeString parses a length-prefixed string starting with the first digit.
func decodeString(r *bufio.Reader) (string, error) {
	lenStr, err := r.ReadString(':')
	if err != nil {
		return "", fmt.Errorf("bencode: read string length: %w", err)
	}
	lenStr = lenStr[:len(lenStr)-1] // trim ':'
	n, err := strconv.Atoi(lenStr)
	if err != nil {
		return "", fmt.Errorf("bencode: invalid string length %q: %w", lenStr, err)
	}
	buf := make([]byte, n)
	if _, err = io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("bencode: read string data: %w", err)
	}
	return string(buf), nil
}

// decodeList parses a list after the leading 'l' has been consumed.
func decodeList(r *bufio.Reader) ([]any, error) {
	var list []any
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("bencode: read list: %w", err)
		}
		if b == 'e' {
			return list, nil
		}
		_ = r.UnreadByte()
		val, err := decode(r)
		if err != nil {
			return nil, err
		}
		list = append(list, val)
	}
}

// decodeDict parses a dictionary after the leading 'd' has been consumed.
func decodeDict(r *bufio.Reader) (map[string]any, error) {
	m := make(map[string]any)
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("bencode: read dict: %w", err)
		}
		if b == 'e' {
			return m, nil
		}
		_ = r.UnreadByte()
		key, err := decodeString(r)
		if err != nil {
			return nil, fmt.Errorf("bencode: dict key: %w", err)
		}
		val, err := decode(r)
		if err != nil {
			return nil, fmt.Errorf("bencode: dict value for %q: %w", key, err)
		}
		m[key] = val
	}
}

// Encode bencodes v and writes the result to w.
// v must be one of: int64, int, string, []byte, []any, or map[string]any.
func Encode(w io.Writer, v any) error {
	switch val := v.(type) {
	case int64:
		_, err := fmt.Fprintf(w, "i%de", val)
		return err
	case int:
		_, err := fmt.Fprintf(w, "i%de", val)
		return err
	case string:
		if _, err := fmt.Fprintf(w, "%d:", len(val)); err != nil {
			return err
		}
		_, err := io.WriteString(w, val)
		return err
	case []byte:
		if _, err := fmt.Fprintf(w, "%d:", len(val)); err != nil {
			return err
		}
		_, err := w.Write(val)
		return err
	case []any:
		if _, err := io.WriteString(w, "l"); err != nil {
			return err
		}
		for _, item := range val {
			if err := Encode(w, item); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "e")
		return err
	case map[string]any:
		if _, err := io.WriteString(w, "d"); err != nil {
			return err
		}
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := Encode(w, k); err != nil {
				return err
			}
			if err := Encode(w, val[k]); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "e")
		return err
	default:
		return fmt.Errorf("bencode: unsupported type %T", v)
	}
}

// EncodeToString bencodes v and returns the result as a string.
func EncodeToString(v any) (string, error) {
	var sb strings.Builder
	if err := Encode(&sb, v); err != nil {
		return "", err
	}
	return sb.String(), nil
}
