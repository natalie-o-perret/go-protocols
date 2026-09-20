// Command gotorrent is a command-line tool for working with BitTorrent files.
//
// Usage:
//
// gotorrent <command> [arguments]
//
// Commands:
//
// info <file.torrent>   Print metadata from a .torrent file.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/natalie-o-perret/go-torrent/metainfo"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "info":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: gotorrent info <file.torrent>")
			os.Exit(1)
		}
		if err := runInfo(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: gotorrent <command> [arguments]")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  info <file.torrent>   print metadata from a .torrent file")
}

func runInfo(args []string) error {
	f, err := os.Open(args[0])
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()

	mi, err := metainfo.Decode(f)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	fmt.Printf("Name:         %s\n", mi.Info.Name)
	fmt.Printf("InfoHash:     %s\n", mi.InfoHash)
	fmt.Printf("PieceLength:  %d\n", mi.Info.PieceLength)
	fmt.Printf("Pieces:       %d\n", mi.Info.PieceCount())
	fmt.Printf("TotalLength:  %d\n", mi.Info.TotalLength())
	if mi.Announce != "" {
		fmt.Printf("Announce:     %s\n", mi.Announce)
	}
	if mi.Comment != "" {
		fmt.Printf("Comment:      %s\n", mi.Comment)
	}
	if mi.CreatedBy != "" {
		fmt.Printf("CreatedBy:    %s\n", mi.CreatedBy)
	}
	if len(mi.Info.Files) > 0 {
		fmt.Printf("Files:\n")
		for _, fi := range mi.Info.Files {
			fmt.Printf("  %s  (%d bytes)\n", joinPath(fi.Path), fi.Length)
		}
	}
	trackers := mi.Trackers()
	if len(trackers) > 0 {
		fmt.Printf("Trackers:\n")
		for _, t := range trackers {
			fmt.Printf("  %s\n", t)
		}
	}
	return nil
}

// joinPath joins a slice of path components with the OS path separator.
func joinPath(parts []string) string {
	return strings.Join(parts, "/")
}
