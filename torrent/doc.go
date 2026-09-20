// Package gotorrent is a BitTorrent protocol library and CLI for Go 1.24+.
//
// It provides focused, composable packages implementing the core BitTorrent
// specification (BEP 3) and related extensions.
//
// # Packages
//
// bencode: Encoder and decoder for the bencoding format used in .torrent files
// and tracker responses -- supports int64, string, []byte, []any, and
// map[string]any.
//
// bitfield: Compact byte-slice bitfield for tracking which pieces a peer has
// downloaded, with O(1) Has/Set and an O(n) popcount.
//
// metainfo: Parser for .torrent files -- extracts the Info struct, computes the
// 20-byte SHA-1 InfoHash, and provides the flat tracker URL list.
//
// tracker: HTTP tracker client -- constructs announce requests and parses compact
// and dictionary peer responses.
//
// peer: BitTorrent peer wire protocol -- handshake (BEP 3), length-prefixed
// message framing, and helpers for the Request and Piece message types.
//
// piece: Per-piece download state machine -- block-level request tracking,
// data accumulation, and SHA-1 hash verification.
package gotorrent
