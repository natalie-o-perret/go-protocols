// Command gotorrent is a command-line tool for working with BitTorrent files.
//
// Usage:
//
// gotorrent <command> [arguments]
//
// Commands:
//
// info <file.torrent>                 Print metadata from a .torrent file.
// download [-o path] <file.torrent>   Download and verify all files.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	gotorrent "github.com/natalie-o-perret/go-protocols/torrent"
	"github.com/natalie-o-perret/go-protocols/torrent/metainfo"
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
	case "download":
		if err := runDownload(os.Args[2:]); err != nil {
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
	fmt.Fprintln(os.Stderr, "  download [-o path] <file.torrent>   download and verify all files")
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

func runDownload(args []string) error {
	flags := flag.NewFlagSet("download", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("o", "", "output file or directory")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("usage: gotorrent download [-o path] <file.torrent>: %w", err)
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: gotorrent download [-o path] <file.torrent>")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	f, err := os.Open(flags.Arg(0))
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	meta, err := metainfo.Decode(f)
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close metainfo: %w", closeErr)
	}
	if err := gotorrent.Validate(meta); err != nil {
		return err
	}

	destination := *output
	if destination == "" {
		if err := validatePathPart(meta.Info.Name); err != nil {
			return fmt.Errorf("invalid torrent name: %w", err)
		}
		destination = meta.Info.Name
	}
	destination = filepath.Clean(destination)
	store, temporary, err := createOutput(ctx, meta, destination)
	if err != nil {
		return err
	}
	cleanup := func() {
		_ = store.Close()
		_ = os.RemoveAll(temporary)
	}
	if err := ctx.Err(); err != nil {
		cleanup()
		return err
	}
	if err := gotorrent.Download(ctx, meta, store); err != nil {
		cleanup()
		return err
	}
	if err := store.Close(); err != nil {
		_ = os.RemoveAll(temporary)
		return fmt.Errorf("close output: %w", err)
	}
	if _, err := os.Lstat(destination); err == nil {
		_ = os.RemoveAll(temporary)
		return fmt.Errorf("output already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = os.RemoveAll(temporary)
		return fmt.Errorf("inspect output: %w", err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.RemoveAll(temporary)
		return fmt.Errorf("publish output: %w", err)
	}

	fmt.Printf("Downloaded %s to %s\n", meta.Info.Name, destination)
	return nil
}

type outputFile struct {
	file       *os.File
	start, end int64
}

type fileStore struct {
	files []outputFile
	total int64
}

func createOutput(ctx context.Context, meta *metainfo.MetaInfo, destination string) (*fileStore, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if len(meta.Info.Files) == 0 {
		if meta.Info.Length < 0 {
			return nil, "", fmt.Errorf("invalid file length %d", meta.Info.Length)
		}
	} else {
		var total int64
		for _, info := range meta.Info.Files {
			if err := ctx.Err(); err != nil {
				return nil, "", err
			}
			if err := validateTorrentPath(info.Path); err != nil {
				return nil, "", err
			}
			if info.Length < 0 || info.Length > math.MaxInt64-total {
				return nil, "", fmt.Errorf("invalid file length %d", info.Length)
			}
			total += info.Length
		}
	}

	if _, err := os.Lstat(destination); err == nil {
		return nil, "", fmt.Errorf("output already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("inspect output: %w", err)
	}

	temporary := destination + ".part"
	if len(meta.Info.Files) == 0 {
		file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
		if err != nil {
			return nil, "", fmt.Errorf("create output: %w", err)
		}
		if err := file.Truncate(meta.Info.Length); err != nil {
			_ = file.Close()
			_ = os.Remove(temporary)
			return nil, "", fmt.Errorf("size output: %w", err)
		}
		return &fileStore{
			files: []outputFile{{file: file, end: meta.Info.Length}},
			total: meta.Info.Length,
		}, temporary, nil
	}

	if err := os.Mkdir(temporary, 0o755); err != nil {
		return nil, "", fmt.Errorf("create output directory: %w", err)
	}
	store := &fileStore{}
	for _, info := range meta.Info.Files {
		if err := ctx.Err(); err != nil {
			_ = store.Close()
			_ = os.RemoveAll(temporary)
			return nil, "", err
		}

		path := filepath.Join(append([]string{temporary}, info.Path...)...)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			_ = store.Close()
			_ = os.RemoveAll(temporary)
			return nil, "", fmt.Errorf("create output directory: %w", err)
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
		if err != nil {
			_ = store.Close()
			_ = os.RemoveAll(temporary)
			return nil, "", fmt.Errorf("create output file: %w", err)
		}
		if err := file.Truncate(info.Length); err != nil {
			_ = file.Close()
			_ = store.Close()
			_ = os.RemoveAll(temporary)
			return nil, "", fmt.Errorf("size output file: %w", err)
		}
		store.files = append(store.files, outputFile{
			file:  file,
			start: store.total,
			end:   store.total + info.Length,
		})
		store.total += info.Length
	}
	return store, temporary, nil
}

func validateTorrentPath(parts []string) error {
	if len(parts) == 0 {
		return fmt.Errorf("invalid empty torrent path")
	}
	for _, part := range parts {
		if err := validatePathPart(part); err != nil {
			return fmt.Errorf("invalid torrent path %q: %w", joinPath(parts), err)
		}
	}
	return nil
}

func validatePathPart(part string) error {
	if part == "" || part == "." || part == ".." || !filepath.IsLocal(part) || filepath.IsAbs(part) ||
		strings.ContainsAny(part, `/\`) || strings.ContainsRune(part, '\x00') {
		return fmt.Errorf("unsafe path component %q", part)
	}
	return nil
}

func (s *fileStore) WriteAt(data []byte, offset int64) (int, error) {
	if offset < 0 || offset > s.total || int64(len(data)) > s.total-offset {
		return 0, fmt.Errorf("write range [%d,%d) exceeds output size %d", offset, offset+int64(len(data)), s.total)
	}

	written := 0
	for len(data) > 0 {
		index := sort.Search(len(s.files), func(i int) bool { return s.files[i].end > offset })
		if index == len(s.files) {
			return written, io.ErrShortWrite
		}
		output := s.files[index]
		length := len(data)
		if available := output.end - offset; int64(length) > available {
			length = int(available)
		}
		n, err := output.file.WriteAt(data[:length], offset-output.start)
		written += n
		offset += int64(n)
		data = data[n:]
		if err != nil {
			return written, err
		}
		if n != length {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func (s *fileStore) Close() error {
	errs := make([]error, 0, len(s.files))
	for _, output := range s.files {
		if err := output.file.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
