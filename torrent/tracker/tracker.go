// Package tracker implements an HTTP tracker client for the BitTorrent
// protocol, as defined in BEP 3 and BEP 23 (compact peer lists).
//
// [Announce] sends a tracker announce request and returns the list of peers
// along with interval and seeder/leecher counts.
package tracker

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/natalie-o-perret/go-torrent/bencode"
	"github.com/natalie-o-perret/go-torrent/metainfo"
)

// Event is the tracker event parameter defined in BEP 3.
type Event string

const (
	// EventNone is used for regular interval announces.
	EventNone Event = ""
	// EventStarted is sent on the first announce for a torrent.
	EventStarted Event = "started"
	// EventStopped is sent when a client gracefully stops downloading.
	EventStopped Event = "stopped"
	// EventCompleted is sent when the download finishes.
	EventCompleted Event = "completed"
)

// AnnounceRequest contains the parameters for a tracker announce request.
type AnnounceRequest struct {
	Event      Event
	Uploaded   int64
	Downloaded int64
	Left       int64
	NumWant    int
	Port       uint16
	InfoHash   metainfo.Hash
	PeerID     [20]byte
}

// Peer represents a peer returned by the tracker.
type Peer struct {
	IP   net.IP
	Port uint16
}

// String returns the peer address as "ip:port".
func (p Peer) String() string {
	return net.JoinHostPort(p.IP.String(), strconv.Itoa(int(p.Port)))
}

// AnnounceResponse contains the parsed response from a tracker announce.
type AnnounceResponse struct {
	TrackerID   string
	Peers       []Peer
	Interval    int
	MinInterval int
	Complete    int
	Incomplete  int
}

// Announce sends an HTTP tracker announce request and returns the parsed response.
func Announce(trackerURL string, req AnnounceRequest) (*AnnounceResponse, error) {
	u, err := url.Parse(trackerURL)
	if err != nil {
		return nil, fmt.Errorf("tracker: parse URL %q: %w", trackerURL, err)
	}

	q := u.Query()
	q.Set("info_hash", string(req.InfoHash[:]))
	q.Set("peer_id", string(req.PeerID[:]))
	q.Set("port", strconv.Itoa(int(req.Port)))
	q.Set("uploaded", strconv.FormatInt(req.Uploaded, 10))
	q.Set("downloaded", strconv.FormatInt(req.Downloaded, 10))
	q.Set("left", strconv.FormatInt(req.Left, 10))
	q.Set("compact", "1")
	if req.NumWant >= 0 {
		q.Set("numwant", strconv.Itoa(req.NumWant))
	}
	if req.Event != EventNone {
		q.Set("event", string(req.Event))
	}
	u.RawQuery = q.Encode()

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("tracker: announce GET: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tracker: read response body: %w", err)
	}

	return parseResponse(body)
}

func parseResponse(data []byte) (*AnnounceResponse, error) {
	raw, err := bencode.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("tracker: decode response: %w", err)
	}
	dict, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tracker: response is not a dictionary")
	}

	if failure, ok := dict["failure reason"]; ok {
		return nil, fmt.Errorf("tracker failure: %s", failure)
	}

	ar := &AnnounceResponse{}
	if v, ok := dict["interval"]; ok {
		if n, ok := v.(int64); ok {
			ar.Interval = int(n)
		}
	}
	if v, ok := dict["min interval"]; ok {
		if n, ok := v.(int64); ok {
			ar.MinInterval = int(n)
		}
	}
	if v, ok := dict["tracker id"]; ok {
		ar.TrackerID, _ = v.(string)
	}
	if v, ok := dict["complete"]; ok {
		if n, ok := v.(int64); ok {
			ar.Complete = int(n)
		}
	}
	if v, ok := dict["incomplete"]; ok {
		if n, ok := v.(int64); ok {
			ar.Incomplete = int(n)
		}
	}

	if v, ok := dict["peers"]; ok {
		switch peers := v.(type) {
		case string:
			// BEP 23 compact format: 6 bytes per IPv4 peer (4-byte IP + 2-byte big-endian port)
			if len(peers)%6 != 0 {
				return nil, fmt.Errorf("tracker: compact peers length %d not a multiple of 6", len(peers))
			}
			for i := 0; i < len(peers); i += 6 {
				ip := net.IP([]byte(peers[i : i+4]))
				port := binary.BigEndian.Uint16([]byte(peers[i+4 : i+6]))
				ar.Peers = append(ar.Peers, Peer{IP: ip, Port: port})
			}
		case []any:
			// Dictionary model (non-compact)
			for _, p := range peers {
				pd, ok := p.(map[string]any)
				if !ok {
					continue
				}
				peer := Peer{}
				if ipStr, ok := pd["ip"].(string); ok {
					peer.IP = net.ParseIP(ipStr)
				}
				if port, ok := pd["port"].(int64); ok {
					peer.Port = uint16(port)
				}
				ar.Peers = append(ar.Peers, peer)
			}
		}
	}

	return ar, nil
}
