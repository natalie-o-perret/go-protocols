# go-torrent

[![CI](https://github.com/natalie-o-perret/go-protocols/actions/workflows/ci.yml/badge.svg)](https://github.com/natalie-o-perret/go-protocols/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/natalie-o-perret/go-protocols/torrent.svg)](https://pkg.go.dev/github.com/natalie-o-perret/go-protocols/torrent)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Contributing](https://img.shields.io/badge/contributions-welcome-brightgreen.svg)](CONTRIBUTING.md)

A focused, composable BitTorrent protocol library for Go 1.26+.

> [!NOTE]
> This library implements the core BitTorrent protocol (BEP 3) with a clean,
> package-per-concern design. No reflection. No global state. Pure stdlib.

## Packages

| Package | Import path | Description |
| --- | --- | --- |
| `gotorrent` | `github.com/natalie-o-perret/go-protocols/torrent` | End-to-end BEP 3 downloader |
| `bencode` | `github.com/natalie-o-perret/go-protocols/torrent/bencode` | Bencoding encoder and decoder |
| `bitfield` | `github.com/natalie-o-perret/go-protocols/torrent/bitfield` | Compact bitfield for piece tracking |
| `metainfo` | `github.com/natalie-o-perret/go-protocols/torrent/metainfo` | `.torrent` file parser and InfoHash computation |
| `tracker` | `github.com/natalie-o-perret/go-protocols/torrent/tracker` | HTTP tracker announce client |
| `peer` | `github.com/natalie-o-perret/go-protocols/torrent/peer` | Peer wire protocol (handshake, messages) |
| `piece` | `github.com/natalie-o-perret/go-protocols/torrent/piece` | Per-piece download state and verification |

## Quick start

```go
import (
    "os"

    "github.com/natalie-o-perret/go-protocols/torrent/metainfo"
    "github.com/natalie-o-perret/go-protocols/torrent/tracker"
)

// Parse a .torrent file
f, _ := os.Open("ubuntu.torrent")
defer f.Close()

m, err := metainfo.Decode(f)
if err != nil {
    // handle
}

fmt.Println(m.Info.Name)    // e.g. "ubuntu-24.04-desktop-amd64.iso"
fmt.Println(m.InfoHash)     // hex SHA-1 of the info dict

// Announce to the first tracker
peers, err := tracker.Announce(m.Trackers()[0], tracker.AnnounceRequest{
    InfoHash: m.InfoHash,
    Left:     m.Info.TotalLength(),
    NumWant:  50,
})
```

## CLI

```sh
go install github.com/natalie-o-perret/go-protocols/torrent/cmd/gotorrent@latest

gotorrent info ubuntu.torrent
gotorrent download ubuntu.torrent
gotorrent download -o ubuntu.iso ubuntu.torrent
```

`download` discovers peers through HTTP(S) trackers, pipelines block requests,
verifies every piece before writing it, and supports both single-file and
multi-file torrents. Output is written to a `.part` path and renamed only after
the full torrent has passed hash verification.

Example output:

```text
Name:         ubuntu-24.04-desktop-amd64.iso
InfoHash:     e4be9e4db876e3e3179778b03e906297be5c8dbe
Piece length: 524288 bytes
Pieces:       4560
Total size:   2392997888 bytes
Announce:     https://torrent.ubuntu.com/announce
Trackers:
  https://torrent.ubuntu.com/announce
  https://ipv6.torrent.ubuntu.com/announce
```

## Protocol coverage

| BEP                                                     | Description                           | Status                          |
| ------------------------------------------------------- | ------------------------------------- | ------------------------------- |
| [BEP 3](https://www.bittorrent.org/beps/bep_0003.html)  | The BitTorrent Protocol Specification | HTTP tracker and peer downloads |
| [BEP 23](https://www.bittorrent.org/beps/bep_0023.html) | Tracker Returns Compact Peer Lists    | `tracker`                       |

Magnet links, UDP trackers, DHT, peer exchange, resumable downloads, and
seeding are not currently implemented. Piece sizes above 64 MiB are rejected
to bound piece-buffer memory use, and metainfo files are capped at 64 MiB.

## License

MIT - see [LICENSE](LICENSE).
