# Copilot code review instructions for go-torrent

## General Go guidelines

- Flag any use of `interface{}` or `any` where a concrete typed alternative exists.
- Every `error` return must be checked; flag ignored errors.
- Ensure all exported functions, types, and constants have doc comments following the `// Name ...` convention.
- Use table-driven tests; flag test functions that lack cases for edge values (zero, empty, boundary).

## Network and protocol safety

- Flag goroutines that write to shared state without a mutex or channel synchronisation.
- Ensure all blocking network operations have a deadline; flag calls to `conn.Read`/`conn.Write` without a prior `SetDeadline`.
- Warn on `select` blocks with no `default` that could block indefinitely without a context.
- Flag unbounded slices accumulating data from the network — prefer fixed-capacity or streaming approaches.

## BitTorrent protocol correctness

- Bencode dict keys must be emitted in lexicographic order; flag any `Encode` path that skips sorting.
- Piece SHA-1 verification must happen before data is considered valid; flag `piece.State.Data()` calls before `piece.State.Verify()`.
- Tracker compact peer format is 6 bytes per peer (4-byte IPv4 + 2-byte big-endian port); flag arithmetic that uses a different stride.
- Peer handshake must validate the InfoHash; flag `Handshake` implementations that skip this check.

## Test coverage

- Every exported function must have at least one test for the happy path and one for a boundary/error case.
- Protocol parsing tests should use hand-crafted byte sequences, not only round-trips.
- Flag calls to `t.Skip` without a corresponding tracking issue reference.

## Dependency hygiene

- This library targets zero runtime dependencies beyond the standard library; flag any new `require` in `go.mod`.
- Warn on `go.sum` changes without a corresponding `go.mod` change.
