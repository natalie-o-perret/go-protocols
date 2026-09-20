// Package sftp implements the SSH File Transfer Protocol as defined
// by draft-ietf-secsh-filexfer-13. It exposes wire-level message
// types and constants, used by both the client and server
// sub-packages.
//
// The package follows the same convention as the rest of the Go
// protocol libraries in this family: low-level primitives only, no
// goroutines, no global state.
package sftp

// Type identifies a packet in the SFTP wire protocol.
type Type byte

const (
	// 1-2 are initialization messages.
	TypeInit    Type = 1
	TypeVersion Type = 2
	// 3-19 are file messages.
	TypeOpen     Type = 3
	TypeClose    Type = 4
	TypeRead     Type = 5
	TypeWrite    Type = 6
	TypeLstat    Type = 7
	TypeFstat    Type = 8
	TypeSetstat  Type = 9
	TypeFsetstat Type = 10
	TypeOpendir  Type = 11
	TypeReaddir  Type = 12
	TypeRemove   Type = 13
	TypeMkdir    Type = 14
	TypeRmdir    Type = 15
	TypeRename   Type = 16
	TypeReadlink Type = 21
	TypeSymlink  Type = 20
	TypeStat     Type = 17
	// 101-107 are reply messages.
	TypeStatus   Type = 101
	TypeHandle   Type = 102
	TypeData     Type = 103
	TypeName     Type = 104
	TypeAttrs    Type = 105
	TypeExtended Type = 200
)

// OpenFlag bits are flags for the OPEN message.
const (
	OpenRead   uint32 = 0x00000001
	OpenWrite  uint32 = 0x00000002
	OpenAppend uint32 = 0x00000004
	OpenCreate uint32 = 0x00000008
	OpenTrunc  uint32 = 0x00000010
	OpenExcl   uint32 = 0x00000020
)

// StatusCode is the result code returned in a STATUS packet.
type StatusCode uint32

const (
	StatusOK               StatusCode = 0
	StatusEOF              StatusCode = 1
	StatusNoSuchFile       StatusCode = 2
	StatusPermissionDenied StatusCode = 3
	StatusFailure          StatusCode = 4
	StatusBadMessage       StatusCode = 5
	StatusNoConnection     StatusCode = 6
	StatusConnectionLost   StatusCode = 7
	StatusOpUnsupported    StatusCode = 8
)
