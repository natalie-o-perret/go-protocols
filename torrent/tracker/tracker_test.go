package tracker_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/natalie-o-perret/go-torrent/bencode"
	"github.com/natalie-o-perret/go-torrent/tracker"
)

func buildCompactPeers(peers []tracker.Peer) string {
	buf := make([]byte, 0, len(peers)*6)
	for _, p := range peers {
		ip4 := p.IP.To4()
		if ip4 == nil {
			continue
		}
		buf = append(buf, ip4...)
		buf = append(buf, byte(p.Port>>8), byte(p.Port))
	}
	return string(buf)
}

func testResponse(t *testing.T, body map[string]any) *httptest.Server {
	t.Helper()
	encoded, err := bencode.EncodeToString(body)
	if err != nil {
		t.Fatalf("encode response: %v", err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(encoded))
	}))
}

func TestAnnounceCompactPeers(t *testing.T) {
	want := []tracker.Peer{
		{IP: net.ParseIP("1.2.3.4"), Port: 6881},
		{IP: net.ParseIP("5.6.7.8"), Port: 51413},
	}
	resp := map[string]any{
		"interval":   int64(1800),
		"complete":   int64(10),
		"incomplete": int64(2),
		"peers":      buildCompactPeers(want),
	}
	srv := testResponse(t, resp)
	defer srv.Close()

	ar, err := tracker.Announce(srv.URL+"/announce", tracker.AnnounceRequest{NumWant: 50})
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}

	if ar.Interval != 1800 {
		t.Errorf("Interval = %d, want 1800", ar.Interval)
	}
	if ar.Complete != 10 {
		t.Errorf("Complete = %d, want 10", ar.Complete)
	}
	if ar.Incomplete != 2 {
		t.Errorf("Incomplete = %d, want 2", ar.Incomplete)
	}
	if len(ar.Peers) != 2 {
		t.Fatalf("len(Peers) = %d, want 2", len(ar.Peers))
	}
	if ar.Peers[0].Port != 6881 {
		t.Errorf("Peers[0].Port = %d, want 6881", ar.Peers[0].Port)
	}
	if ar.Peers[1].Port != 51413 {
		t.Errorf("Peers[1].Port = %d, want 51413", ar.Peers[1].Port)
	}
}

func TestAnnounceDictionaryPeers(t *testing.T) {
	resp := map[string]any{
		"interval": int64(900),
		"peers": []any{
			map[string]any{"ip": "192.168.1.1", "port": int64(6881)},
			map[string]any{"ip": "10.0.0.1", "port": int64(6882)},
		},
	}
	srv := testResponse(t, resp)
	defer srv.Close()

	ar, err := tracker.Announce(srv.URL+"/announce", tracker.AnnounceRequest{NumWant: -1})
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
	if len(ar.Peers) != 2 {
		t.Fatalf("len(Peers) = %d, want 2", len(ar.Peers))
	}
	if ar.Peers[0].Port != 6881 {
		t.Errorf("Peers[0].Port = %d, want 6881", ar.Peers[0].Port)
	}
}

func TestAnnounceFailureReason(t *testing.T) {
	resp := map[string]any{
		"failure reason": "info_hash not found",
	}
	srv := testResponse(t, resp)
	defer srv.Close()

	_, err := tracker.Announce(srv.URL+"/announce", tracker.AnnounceRequest{})
	if err == nil {
		t.Error("want error for failure reason, got nil")
	}
}

func TestAnnounceInvalidURL(t *testing.T) {
	_, err := tracker.Announce("://invalid", tracker.AnnounceRequest{})
	if err == nil {
		t.Error("want error for invalid URL, got nil")
	}
}

func TestAnnounceCompactOddLength(t *testing.T) {
	resp := map[string]any{
		"interval": int64(1800),
		"peers":    "abcde",
	}
	srv := testResponse(t, resp)
	defer srv.Close()

	_, err := tracker.Announce(srv.URL+"/announce", tracker.AnnounceRequest{})
	if err == nil {
		t.Error("want error for compact peers with odd length, got nil")
	}
}

func TestPeerString(t *testing.T) {
	p := tracker.Peer{IP: net.ParseIP("1.2.3.4"), Port: 6881}
	if p.String() != "1.2.3.4:6881" {
		t.Errorf("Peer.String() = %q, want 1.2.3.4:6881", p.String())
	}
}

func TestAnnounceEmptyPeers(t *testing.T) {
	resp := map[string]any{
		"interval": int64(1800),
		"peers":    "",
	}
	srv := testResponse(t, resp)
	defer srv.Close()

	ar, err := tracker.Announce(srv.URL+"/announce", tracker.AnnounceRequest{})
	if err != nil {
		t.Fatalf("Announce: %v", err)
	}
	if len(ar.Peers) != 0 {
		t.Errorf("len(Peers) = %d, want 0", len(ar.Peers))
	}
}
