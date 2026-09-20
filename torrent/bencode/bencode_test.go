package bencode_test

import (
	"strings"
	"testing"

	"github.com/natalie-o-perret/go-torrent/bencode"
)

func TestDecodeInt(t *testing.T) {
	tests := []struct {
		in   string
		want int64
		err  bool
	}{
		{"i42e", 42, false},
		{"i0e", 0, false},
		{"i-1e", -1, false},
		{"i1000000e", 1_000_000, false},
		{"i-0e", 0, true},
		{"ixe", 0, true},
		{"i9999999999999999999e", 0, true},
	}
	for _, tc := range tests {
		got, err := bencode.Decode(strings.NewReader(tc.in))
		if tc.err {
			if err == nil {
				t.Errorf("Decode(%q): want error, got %v", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Decode(%q): unexpected error: %v", tc.in, err)
			continue
		}
		n, ok := got.(int64)
		if !ok {
			t.Errorf("Decode(%q): got %T, want int64", tc.in, got)
			continue
		}
		if n != tc.want {
			t.Errorf("Decode(%q) = %d, want %d", tc.in, n, tc.want)
		}
	}
}

func TestDecodeString(t *testing.T) {
	tests := []struct {
		in   string
		want string
		err  bool
	}{
		{"4:spam", "spam", false},
		{"0:", "", false},
		{"3:abc", "abc", false},
		{"5:ab", "", true},
	}
	for _, tc := range tests {
		got, err := bencode.Decode(strings.NewReader(tc.in))
		if tc.err {
			if err == nil {
				t.Errorf("Decode(%q): want error, got %v", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Decode(%q): unexpected error: %v", tc.in, err)
			continue
		}
		s, ok := got.(string)
		if !ok {
			t.Errorf("Decode(%q): got %T, want string", tc.in, got)
			continue
		}
		if s != tc.want {
			t.Errorf("Decode(%q) = %q, want %q", tc.in, s, tc.want)
		}
	}
}

func TestDecodeList(t *testing.T) {
	got, err := bencode.Decode(strings.NewReader("l4:spami42ee"))
	if err != nil {
		t.Fatal(err)
	}
	list, ok := got.([]any)
	if !ok {
		t.Fatalf("got %T, want []any", got)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	if list[0] != "spam" {
		t.Errorf("list[0] = %v, want spam", list[0])
	}
	if list[1] != int64(42) {
		t.Errorf("list[1] = %v, want 42", list[1])
	}
}

func TestDecodeEmptyList(t *testing.T) {
	got, err := bencode.Decode(strings.NewReader("le"))
	if err != nil {
		t.Fatal(err)
	}
	list, ok := got.([]any)
	if !ok {
		t.Fatalf("got %T, want []any", got)
	}
	if len(list) != 0 {
		t.Errorf("len = %d, want 0", len(list))
	}
}

func TestDecodeDict(t *testing.T) {
	got, err := bencode.Decode(strings.NewReader("d3:bar4:spam3:fooi42ee"))
	if err != nil {
		t.Fatal(err)
	}
	d, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map[string]any", got)
	}
	if d["bar"] != "spam" {
		t.Errorf("bar = %v, want spam", d["bar"])
	}
	if d["foo"] != int64(42) {
		t.Errorf("foo = %v, want 42", d["foo"])
	}
}

func TestDecodeEmptyDict(t *testing.T) {
	got, err := bencode.Decode(strings.NewReader("de"))
	if err != nil {
		t.Fatal(err)
	}
	d, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map[string]any", got)
	}
	if len(d) != 0 {
		t.Errorf("len = %d, want 0", len(d))
	}
}

func TestDecodeNestedList(t *testing.T) {
	got, err := bencode.Decode(strings.NewReader("ll4:spamee"))
	if err != nil {
		t.Fatal(err)
	}
	outer, ok := got.([]any)
	if !ok {
		t.Fatalf("got %T, want []any", got)
	}
	if len(outer) != 1 {
		t.Fatalf("outer len = %d, want 1", len(outer))
	}
	inner, ok := outer[0].([]any)
	if !ok {
		t.Fatalf("inner is %T, want []any", outer[0])
	}
	if inner[0] != "spam" {
		t.Errorf("inner[0] = %v, want spam", inner[0])
	}
}

func TestDecodeUnexpectedByte(t *testing.T) {
	_, err := bencode.Decode(strings.NewReader("x"))
	if err == nil {
		t.Error("want error for unknown prefix byte, got nil")
	}
}

func TestEncodeInt(t *testing.T) {
	tests := []struct {
		want string
		val  int64
	}{
		{want: "i0e", val: 0},
		{want: "i42e", val: 42},
		{want: "i-1e", val: -1},
	}
	for _, tc := range tests {
		s, err := bencode.EncodeToString(tc.val)
		if err != nil {
			t.Errorf("EncodeToString(%d): %v", tc.val, err)
			continue
		}
		if s != tc.want {
			t.Errorf("EncodeToString(%d) = %q, want %q", tc.val, s, tc.want)
		}
	}
}

func TestEncodeString(t *testing.T) {
	tests := []struct {
		val  string
		want string
	}{
		{"spam", "4:spam"},
		{"", "0:"},
		{"abc", "3:abc"},
	}
	for _, tc := range tests {
		s, err := bencode.EncodeToString(tc.val)
		if err != nil {
			t.Errorf("EncodeToString(%q): %v", tc.val, err)
			continue
		}
		if s != tc.want {
			t.Errorf("EncodeToString(%q) = %q, want %q", tc.val, s, tc.want)
		}
	}
}

func TestEncodeList(t *testing.T) {
	s, err := bencode.EncodeToString([]any{"spam", int64(42)})
	if err != nil {
		t.Fatal(err)
	}
	if s != "l4:spami42ee" {
		t.Errorf("got %q, want l4:spami42ee", s)
	}
}

func TestEncodeDict(t *testing.T) {
	s, err := bencode.EncodeToString(map[string]any{
		"foo": int64(42),
		"bar": "spam",
	})
	if err != nil {
		t.Fatal(err)
	}
	if s != "d3:bar4:spam3:fooi42ee" {
		t.Errorf("got %q, want d3:bar4:spam3:fooi42ee", s)
	}
}

func TestRoundTrip(t *testing.T) {
	tests := []string{
		"i0e",
		"i-1e",
		"4:spam",
		"0:",
		"l4:spami42ee",
		"d3:bar4:spam3:fooi42ee",
		"le",
		"de",
	}
	for _, tc := range tests {
		got, err := bencode.Decode(strings.NewReader(tc))
		if err != nil {
			t.Errorf("Decode(%q): %v", tc, err)
			continue
		}
		s, err := bencode.EncodeToString(got)
		if err != nil {
			t.Errorf("EncodeToString for %q: %v", tc, err)
			continue
		}
		if s != tc {
			t.Errorf("round-trip(%q) = %q", tc, s)
		}
	}
}

func TestEncodeUnsupportedType(t *testing.T) {
	_, err := bencode.EncodeToString(struct{}{})
	if err == nil {
		t.Error("want error for unsupported type, got nil")
	}
}

func TestEncodeBytes(t *testing.T) {
	s, err := bencode.EncodeToString([]byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if s != "3:abc" {
		t.Errorf("got %q, want 3:abc", s)
	}
}
