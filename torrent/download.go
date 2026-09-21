package gotorrent

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/natalie-o-perret/go-protocols/torrent/bitfield"
	"github.com/natalie-o-perret/go-protocols/torrent/metainfo"
	"github.com/natalie-o-perret/go-protocols/torrent/peer"
	"github.com/natalie-o-perret/go-protocols/torrent/piece"
	"github.com/natalie-o-perret/go-protocols/torrent/tracker"
)

const (
	announcePort = 6881
	maxPieceSize = 64 << 20
	maxBacklog   = 5
	peerTimeout  = 30 * time.Second
)

var errPieceUnavailable = errors.New("peer does not have piece")

// Download retrieves and verifies all pieces in meta, writing the torrent's
// contiguous byte stream to dst. Multi-file callers map that stream to files.
func Download(ctx context.Context, meta *metainfo.MetaInfo, dst io.WriterAt) error {
	if dst == nil {
		return fmt.Errorf("torrent: nil destination")
	}

	total, err := validateMetaInfo(meta)
	if err != nil {
		return err
	}
	if total == 0 {
		return nil
	}

	peerID, err := newPeerID()
	if err != nil {
		return fmt.Errorf("torrent: generate peer ID: %w", err)
	}
	trackers := meta.Trackers()
	if len(trackers) == 0 {
		return fmt.Errorf("torrent: no trackers")
	}

	complete := make([]bool, len(meta.Info.Pieces))
	attempted := make(map[string]bool)
	announced := make([]string, 0, len(trackers))
	var downloaded int64
	var failures []error
	finished := false
	defer func() {
		if finished || len(announced) == 0 {
			return
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		announceEvent(stopCtx, announced, meta, peerID, tracker.EventStopped, downloaded, total-downloaded)
	}()

	for _, trackerURL := range trackers {
		if err := ctx.Err(); err != nil {
			return err
		}
		response, err := tracker.AnnounceContext(ctx, trackerURL, tracker.AnnounceRequest{
			Event:      tracker.EventStarted,
			Downloaded: downloaded,
			Left:       total - downloaded,
			NumWant:    50,
			Port:       announcePort,
			InfoHash:   meta.InfoHash,
			PeerID:     peerID,
		})
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			failures = append(failures, fmt.Errorf("announce %s: %w", trackerURL, err))
			continue
		}
		announced = append(announced, trackerURL)
		if len(response.Peers) == 0 {
			failures = append(failures, fmt.Errorf("announce %s: no peers", trackerURL))
			continue
		}

		for _, candidate := range response.Peers {
			if err := ctx.Err(); err != nil {
				return err
			}
			if (candidate.IP == nil && candidate.Host == "") || candidate.Port == 0 {
				continue
			}
			address := candidate.String()
			if attempted[address] {
				continue
			}
			attempted[address] = true

			session, err := dialPeer(ctx, address, meta.InfoHash, peerID, len(complete))
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				failures = append(failures, fmt.Errorf("peer %s: %w", address, err))
				continue
			}

			for index, hash := range meta.Info.Pieces {
				if complete[index] || !session.hasPiece(index) {
					continue
				}
				length := pieceLength(meta, index, total)
				data, err := session.downloadPiece(ctx, index, hash, length)
				if errors.Is(err, errPieceUnavailable) {
					continue
				}
				if err != nil {
					if ctxErr := ctx.Err(); ctxErr != nil {
						_ = session.close()
						return ctxErr
					}
					failures = append(failures, fmt.Errorf("peer %s: %w", address, err))
					break
				}

				offset := int64(index) * meta.Info.PieceLength
				n, err := dst.WriteAt(data, offset)
				if err != nil {
					_ = session.close()
					return fmt.Errorf("torrent: write piece %d: %w", index, err)
				}
				if n != len(data) {
					_ = session.close()
					return fmt.Errorf("torrent: write piece %d: %w", index, io.ErrShortWrite)
				}
				complete[index] = true
				downloaded += int64(len(data))
			}
			_ = session.close()

			if downloaded == total {
				finished = true
				completedCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				announceEvent(completedCtx, announced, meta, peerID, tracker.EventCompleted, total, 0)
				cancel()
				return nil
			}
		}
	}

	err = fmt.Errorf("torrent: downloaded %d of %d pieces", countComplete(complete), len(complete))
	if len(failures) == 0 {
		return err
	}
	return fmt.Errorf("%w: %w", err, errors.Join(failures...))
}

// Validate checks whether meta can be downloaded without opening network or
// filesystem resources.
func Validate(meta *metainfo.MetaInfo) error {
	_, err := validateMetaInfo(meta)
	return err
}

func validateMetaInfo(meta *metainfo.MetaInfo) (int64, error) {
	if meta == nil {
		return 0, fmt.Errorf("torrent: nil metainfo")
	}
	info := &meta.Info
	if err := info.Validate(); err != nil {
		return 0, err
	}
	if info.PieceLength > maxPieceSize {
		return 0, fmt.Errorf("torrent: piece length %d is too large", info.PieceLength)
	}
	return info.TotalLength(), nil
}

func newPeerID() ([20]byte, error) {
	var id [20]byte
	copy(id[:], "-GP0001-")
	_, err := rand.Read(id[8:])
	return id, err
}

func pieceLength(meta *metainfo.MetaInfo, index int, total int64) int {
	remaining := total - int64(index)*meta.Info.PieceLength
	return int(min(meta.Info.PieceLength, remaining))
}

func countComplete(pieces []bool) int {
	var count int
	for _, complete := range pieces {
		if complete {
			count++
		}
	}
	return count
}

func announceEvent(
	ctx context.Context,
	trackers []string,
	meta *metainfo.MetaInfo,
	peerID [20]byte,
	event tracker.Event,
	downloaded, left int64,
) {
	for _, trackerURL := range trackers {
		_, _ = tracker.AnnounceContext(ctx, trackerURL, tracker.AnnounceRequest{
			Event:      event,
			Downloaded: downloaded,
			Left:       left,
			NumWant:    0,
			Port:       announcePort,
			InfoHash:   meta.InfoHash,
			PeerID:     peerID,
		})
	}
}

type peerSession struct {
	conn        net.Conn
	pieces      bitfield.Bitfield
	stopCancel  func() bool
	pieceCount  int
	piecesKnown bool
	choked      bool
}

func dialPeer(ctx context.Context, address string, infoHash metainfo.Hash, peerID [20]byte, pieceCount int) (*peerSession, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	session := &peerSession{conn: conn, pieceCount: pieceCount, choked: true}
	session.stopCancel = context.AfterFunc(ctx, func() { _ = conn.Close() })
	if _, err := peer.Handshake(conn, infoHash, peerID); err != nil {
		_ = session.close()
		return nil, err
	}
	if err := session.write(ctx, &peer.Message{ID: peer.MsgInterested}); err != nil {
		_ = session.close()
		return nil, err
	}
	return session, nil
}

func (s *peerSession) hasPiece(index int) bool {
	return !s.piecesKnown || s.pieces.Has(index)
}

func (s *peerSession) downloadPiece(ctx context.Context, index int, hash metainfo.Hash, length int) ([]byte, error) {
	if !s.hasPiece(index) {
		return nil, errPieceUnavailable
	}

	state := piece.New(index, hash, length)
	pending := make(map[uint32]int)
	progressDeadline := time.Now().Add(peerTimeout)
	for !state.Complete() {
		for !s.choked && len(pending) < maxBacklog {
			begin, blockLength, ok := state.NextRequest()
			if !ok {
				break
			}
			if err := s.write(ctx, &peer.Message{
				ID:      peer.MsgRequest,
				Payload: peer.FormatRequest(uint32(index), uint32(begin), uint32(blockLength)),
			}); err != nil {
				return nil, err
			}
			pending[uint32(begin)] = blockLength
		}

		message, err := s.read(ctx, progressDeadline)
		if err != nil {
			return nil, err
		}
		if message == nil {
			continue
		}
		assumedEmpty := !s.piecesKnown && message.ID != peer.MsgBitfield
		if assumedEmpty {
			s.pieces = bitfield.New(s.pieceCount)
			s.piecesKnown = true
		}

		switch message.ID {
		case peer.MsgChoke:
			s.choked = true
			state.ResetRequests()
			clear(pending)
		case peer.MsgUnchoke:
			s.choked = false
		case peer.MsgBitfield:
			if s.piecesKnown {
				return nil, fmt.Errorf("piece %d: duplicate bitfield", index)
			}
			pieces := bitfield.Bitfield(append([]byte(nil), message.Payload...))
			if err := pieces.Validate(s.pieceCount); err != nil {
				return nil, err
			}
			s.pieces = pieces
			s.piecesKnown = true
			if !pieces.Has(index) {
				if len(pending) != 0 {
					return nil, fmt.Errorf("piece %d: peer advertised it as unavailable after accepting requests", index)
				}
				return nil, errPieceUnavailable
			}
		case peer.MsgHave:
			have, err := peer.ParseHave(message.Payload)
			if err != nil {
				return nil, err
			}
			if have >= uint32(s.pieceCount) {
				return nil, fmt.Errorf("peer: have index %d out of range", have)
			}
			if s.pieces == nil {
				s.pieces = bitfield.New(s.pieceCount)
			}
			s.pieces.Set(int(have))
		case peer.MsgPiece:
			pieceIndex, begin, data, err := peer.ParsePiece(message.Payload)
			if err != nil {
				return nil, err
			}
			if pieceIndex != uint32(index) {
				continue
			}
			blockLength, ok := pending[begin]
			if !ok {
				continue
			}
			if len(data) != blockLength {
				return nil, fmt.Errorf("piece %d: block at offset %d has len %d, want %d", index, begin, len(data), blockLength)
			}
			if err := state.Store(int(begin), data); err != nil {
				return nil, err
			}
			delete(pending, begin)
			progressDeadline = time.Now().Add(peerTimeout)
		}
		if assumedEmpty && !s.pieces.Has(index) && len(pending) == 0 {
			return nil, errPieceUnavailable
		}
	}

	if err := state.Verify(); err != nil {
		return nil, err
	}
	return state.Data(), nil
}

func (s *peerSession) read(ctx context.Context, deadline time.Time) (*peer.Message, error) {
	if err := s.setDeadline(ctx, deadline); err != nil {
		return nil, err
	}
	message, err := peer.ReadMessage(s.conn)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	return message, nil
}

func (s *peerSession) write(ctx context.Context, message *peer.Message) error {
	if err := s.setDeadline(ctx, time.Now().Add(peerTimeout)); err != nil {
		return err
	}
	if err := peer.WriteMessage(s.conn, message); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	return nil
}

func (s *peerSession) setDeadline(ctx context.Context, deadline time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	return s.conn.SetDeadline(deadline)
}

func (s *peerSession) close() error {
	if s.stopCancel != nil {
		s.stopCancel()
	}
	return s.conn.Close()
}
