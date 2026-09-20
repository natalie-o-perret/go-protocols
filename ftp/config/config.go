// Package config loads the TOML configuration consumed by cmd/ftpd.
//
// A zero-value File is a valid configuration thanks to Default; the
// loader only fills in fields that are explicitly set in the file.
package config

import (
	"fmt"
	"io"

	"github.com/BurntSushi/toml"
)

// File is the on-disk representation of ftpd.toml.
type File struct {
	Server serverBlock
	Users  []userBlock
	Root   string
}

type serverBlock struct {
	Name             string
	Listen           string
	Banner           string
	WelcomeMessage   string
	PassivePortStart int
	PassivePortEnd   int
}

type userBlock struct {
	Name     string
	Password string
}

// Default returns a File with sensible defaults.
func Default() *File {
	return &File{
		Server: serverBlock{
			Name:   "go-ftp",
			Listen: ":21",
			Banner: "Welcome to go-ftp",
		},
		Root: ".",
	}
}

// Load decodes a TOML config from r into f. Fields not present in
// the file are left at their current value.
func Load(r io.Reader, f *File) error {
	if f == nil {
		f = Default()
	}
	meta, err := toml.NewDecoder(r).Decode(f)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	_ = meta
	return nil
}
