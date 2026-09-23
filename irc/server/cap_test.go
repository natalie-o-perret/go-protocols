package server

import (
	"bytes"
	"encoding/base64"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/natalie-o-perret/go-protocols/irc/irc"
	"golang.org/x/crypto/bcrypt"
)

type recordingConn struct{ bytes.Buffer }

func (*recordingConn) Close() error                     { return nil }
func (*recordingConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (*recordingConn) RemoteAddr() net.Addr             { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1} }
func (*recordingConn) SetDeadline(time.Time) error      { return nil }
func (*recordingConn) SetReadDeadline(time.Time) error  { return nil }
func (*recordingConn) SetWriteDeadline(time.Time) error { return nil }

func TestCAPLS302AdvertisesImplementedCapabilities(t *testing.T) {
	srv := New(Config{Name: "irc.test", Caps: []string{"fake-cap"}})
	conn := &recordingConn{}
	s := newSession(conn, srv)
	srv.handleCAP(s, irc.MustParse("CAP LS 302"))

	line := conn.String()
	if strings.Contains(line, "sasl") || strings.Contains(line, "fake-cap") {
		t.Fatalf("advertised unsupported capability: %q", line)
	}
	msg := irc.MustParse(strings.TrimSpace(line))
	got := strings.Fields(msg.Trailing())
	want := []string{
		irc.CapAwayNotify, irc.CapBatch, irc.CapCapNotify, irc.CapChatHistory,
		irc.CapEchoMessage, irc.CapExtendedJoin, irc.CapInviteNotify,
		irc.CapMessageTags, irc.CapMultiPrefix, irc.CapNoImplicitNames,
		irc.CapServerTime, irc.CapSetname, irc.CapStandardReplies,
		irc.CapUserHostInNames, irc.CapAccountTag, irc.CapAccountNotify,
		irc.CapChghost, irc.CapExtendedMonitor, irc.CapLabeledResponse,
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("advertised capabilities: got %v want %v", got, want)
	}
	if !s.capEnabled(irc.CapCapNotify) || s.state != StateCapNeg {
		t.Fatalf("CAP 302 state: caps=%v state=%v", s.caps, s.state)
	}
}

func TestLabeledResponses(t *testing.T) {
	srv := New(Config{Name: "irc.test"})
	conn := &recordingConn{}
	s := newSession(conn, srv)
	s.nick, s.user, s.state = "alice", "user", StateRegistered
	s.caps[irc.CapBatch] = true
	s.caps[irc.CapLabeledResponse] = true
	s.caps[irc.CapEchoMessage] = true
	if err := srv.sessions.Add(s); err != nil {
		t.Fatal(err)
	}

	srv.dispatchCommand(s, irc.MustParse("@label=missing PRIVMSG nobody :hello"))
	if got := conn.String(); !strings.Contains(got, "@label=missing ") || !strings.Contains(got, " 401 ") {
		t.Fatalf("single labeled response: %q", got)
	}
	conn.Reset()
	srv.dispatchCommand(s, irc.MustParse("@label=ack PONG token"))
	if got := conn.String(); !strings.Contains(got, "@label=ack :irc.test ACK") {
		t.Fatalf("labeled ACK: %q", got)
	}
	conn.Reset()
	srv.dispatchCommand(s, irc.MustParse("@label=who WHOIS alice"))
	if got := conn.String(); !strings.Contains(got, "@label=who :irc.test BATCH +") || !strings.Contains(got, " labeled-response") || !strings.Contains(got, "@batch=") {
		t.Fatalf("batched labeled response: %q", got)
	}
	conn.Reset()
	srv.dispatchCommand(s, irc.MustParse("@label=self PRIVMSG alice :hello"))
	lines := strings.Split(strings.TrimSpace(conn.String()), "\r\n")
	if len(lines) != 2 || strings.Contains(lines[0], "label=self") || !strings.Contains(lines[1], "label=self") {
		t.Fatalf("self-message labeling: %q", lines)
	}
}

func TestSASLPlainAndAccountCapabilities(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{Name: "irc.test", Accounts: map[string]Account{
		"alice": {Name: "Alice", PasswordHash: string(hash), Host: "alice.example"},
	}})
	conn := &recordingConn{}
	s := newSession(conn, srv)
	s.nick, s.user, s.state, s.secure = "alice", "user", StateCapNeg, true
	s.caps[irc.CapSASL] = true

	srv.handleCAP(s, irc.MustParse("CAP LS 302"))
	if !strings.Contains(conn.String(), "sasl=PLAIN") {
		t.Fatalf("SASL not advertised over TLS: %q", conn.String())
	}
	conn.Reset()
	srv.handleAuthenticate(s, irc.MustParse("AUTHENTICATE PLAIN"))
	payload := base64.StdEncoding.EncodeToString([]byte("\x00alice\x00secret"))
	srv.handleAuthenticate(s, &irc.Message{Params: []string{payload}})
	if s.account != "Alice" || s.host != "alice.example" {
		t.Fatalf("authenticated identity: account=%q host=%q", s.account, s.host)
	}
	if got := conn.String(); !strings.Contains(got, "AUTHENTICATE +") || !strings.Contains(got, " 900 ") || !strings.Contains(got, " 903 ") {
		t.Fatalf("SASL exchange: %q", got)
	}

	plain := newSession(&recordingConn{}, srv)
	srv.handleCAP(plain, irc.MustParse("CAP LS 302"))
	if strings.Contains(plain.conn.(*recordingConn).String(), "sasl") {
		t.Fatal("SASL advertised on plaintext connection")
	}
}

func TestAccountAndHostNotifications(t *testing.T) {
	srv := New(Config{Name: "irc.test"})
	alice := newSession(&recordingConn{}, srv)
	bobConn := &recordingConn{}
	bob := newSession(bobConn, srv)
	alice.nick, alice.user, alice.host, alice.state = "alice", "user", "old.example", StateRegistered
	bob.nick, bob.user, bob.state = "bob", "user", StateRegistered
	bob.caps[irc.CapAccountNotify] = true
	bob.caps[irc.CapAccountTag] = true
	bob.caps[irc.CapChghost] = true
	if err := srv.sessions.Add(alice); err != nil {
		t.Fatal(err)
	}
	if err := srv.sessions.Add(bob); err != nil {
		t.Fatal(err)
	}
	ch := srv.channels.GetOrCreate("#test")
	ch.AddMember(alice, "@")
	ch.AddMember(bob, "")

	oldPrefix := alice.Prefix()
	alice.account = "Alice"
	srv.notifyAccount(alice, oldPrefix)
	alice.host = "new.example"
	srv.notifyHostChange(alice, oldPrefix)
	got := bobConn.String()
	if !strings.Contains(got, "@account=Alice :alice!user@old.example ACCOUNT Alice") || !strings.Contains(got, " CHGHOST user new.example") {
		t.Fatalf("identity notifications: %q", got)
	}
}

func TestHistoryPreservesAccountTag(t *testing.T) {
	srv := New(Config{Name: "irc.test"})
	alice := newSession(&recordingConn{}, srv)
	bobConn := &recordingConn{}
	bob := newSession(bobConn, srv)
	alice.nick, alice.user, alice.account, alice.state = "alice", "user", "Alice", StateRegistered
	bob.nick, bob.user, bob.state = "bob", "user", StateRegistered
	if err := srv.sessions.Add(alice); err != nil {
		t.Fatal(err)
	}
	if err := srv.sessions.Add(bob); err != nil {
		t.Fatal(err)
	}
	ch := srv.channels.GetOrCreate("#test")
	ch.AddMember(alice, "@")
	ch.AddMember(bob, "")
	srv.handlePrivmsg(alice, irc.MustParse("PRIVMSG #test :hello"), false)
	entries := ch.historySnapshot()
	if len(entries) != 1 || entries[0].msg.Tags["account"] != "Alice" {
		t.Fatalf("stored account tag: %#v", entries)
	}
	srv.sessions.Remove("alice")
	impostor := newSession(&recordingConn{}, srv)
	impostor.nick, impostor.user, impostor.account = "alice", "other", "Mallory"
	if err := srv.sessions.Add(impostor); err != nil {
		t.Fatal(err)
	}
	bob.caps[irc.CapAccountTag] = true
	bob.Send(entries[0].msg)
	if got := bobConn.String(); !strings.Contains(got, "account=Alice") || strings.Contains(got, "account=Mallory") {
		t.Fatalf("replayed account tag: %q", got)
	}
}

func TestServerRejectsMixedBcryptCosts(t *testing.T) {
	hash4, _ := bcrypt.GenerateFromPassword([]byte("one"), bcrypt.MinCost)
	hash5, _ := bcrypt.GenerateFromPassword([]byte("two"), bcrypt.MinCost+1)
	srv := New(Config{Accounts: map[string]Account{
		"one": {Name: "one", PasswordHash: string(hash4)},
		"two": {Name: "two", PasswordHash: string(hash5)},
	}})
	if _, err := srv.ListenRandom(); err == nil {
		t.Fatal("mixed bcrypt costs accepted")
	}
}

func TestMonitorAndExtendedMonitor(t *testing.T) {
	srv := New(Config{Name: "irc.test"})
	alice := newSession(&recordingConn{}, srv)
	watcherConn := &recordingConn{}
	watcher := newSession(watcherConn, srv)
	alice.nick, alice.user, alice.state = "alice", "user", StateRegistered
	watcher.nick, watcher.user, watcher.state = "watcher", "user", StateRegistered
	watcher.caps[irc.CapExtendedMonitor] = true
	watcher.caps[irc.CapAwayNotify] = true
	if err := srv.sessions.Add(alice); err != nil {
		t.Fatal(err)
	}
	if err := srv.sessions.Add(watcher); err != nil {
		t.Fatal(err)
	}

	srv.handleMonitor(watcher, irc.MustParse("MONITOR + alice,missing"))
	if got := watcherConn.String(); !strings.Contains(got, " 730 ") || !strings.Contains(got, " 731 ") {
		t.Fatalf("MONITOR status: %q", got)
	}
	watcherConn.Reset()
	srv.handleAway(alice, irc.MustParse("AWAY :gone"))
	if got := watcherConn.String(); !strings.Contains(got, " AWAY gone") {
		t.Fatalf("extended-monitor AWAY: %q", got)
	}
}

func TestSTSAdvertisement(t *testing.T) {
	srv := New(Config{Name: "irc.test", STS: &STSConfig{Port: 6697, Duration: time.Hour, Preload: true, Hostnames: []string{"irc.test"}}})
	plainConn := &recordingConn{}
	plain := newSession(plainConn, srv)
	srv.handleCAP(plain, irc.MustParse("CAP LS 302"))
	if !strings.Contains(plainConn.String(), "sts=port=6697") {
		t.Fatalf("plaintext STS: %q", plainConn.String())
	}

	secureConn := &recordingConn{}
	secure := newSession(secureConn, srv)
	secure.secure, secure.serverName = true, "irc.test"
	srv.handleCAP(secure, irc.MustParse("CAP LS 302"))
	if !strings.Contains(secureConn.String(), "sts=duration=3600,preload") {
		t.Fatalf("secure STS: %q", secureConn.String())
	}
	secureConn.Reset()
	srv.handleCAP(secure, irc.MustParse("CAP REQ :sts message-tags"))
	if !strings.Contains(secureConn.String(), " NAK :sts message-tags") || secure.capEnabled(irc.CapMessageTags) {
		t.Fatalf("STS request was not atomically rejected: %q", secureConn.String())
	}
}

func TestCAPREQIsAtomic(t *testing.T) {
	srv := New(Config{Name: "irc.test"})
	conn := &recordingConn{}
	s := newSession(conn, srv)
	s.caps[irc.CapServerTime] = true
	srv.handleCAP(s, irc.MustParse("CAP REQ :message-tags unsupported"))

	if s.capEnabled(irc.CapMessageTags) || !s.capEnabled(irc.CapServerTime) {
		t.Fatalf("CAP NAK changed capabilities: %v", s.caps)
	}
	if !strings.Contains(conn.String(), " CAP * NAK :message-tags unsupported") {
		t.Fatalf("unexpected CAP response: %q", conn.String())
	}
}

func TestCAPListSplitsLongReplies(t *testing.T) {
	srv := New(Config{Name: "irc.test"})
	conn := &recordingConn{}
	s := newSession(conn, srv)
	caps := make([]string, 60)
	for i := range caps {
		caps[i] = strings.Repeat("c", 10)
	}

	srv.sendCAPList(s, irc.CapLS, caps)
	lines := strings.Split(strings.TrimSpace(conn.String()), "\r\n")
	if len(lines) != 2 || irc.MustParse(lines[0]).Param(2) != "*" || irc.MustParse(lines[1]).Param(2) == "*" {
		t.Fatalf("multiline CAP LS: %q", lines)
	}
}

func TestImplementedCapabilityBehaviour(t *testing.T) {
	srv := New(Config{Name: "irc.test"})
	aliceConn, bobConn := &recordingConn{}, &recordingConn{}
	alice, bob := newSession(aliceConn, srv), newSession(bobConn, srv)
	alice.nick, alice.user, alice.realname, alice.state = "alice", "a", "Alice", StateRegistered
	bob.nick, bob.user, bob.realname, bob.state = "bob", "b", "Bob", StateRegistered
	alice.caps[irc.CapExtendedJoin] = true
	alice.caps[irc.CapNoImplicitNames] = true
	alice.caps[irc.CapServerTime] = true
	bob.caps[irc.CapUserHostInNames] = true
	bob.caps[irc.CapMultiPrefix] = true
	if err := srv.sessions.Add(alice); err != nil {
		t.Fatal(err)
	}
	if err := srv.sessions.Add(bob); err != nil {
		t.Fatal(err)
	}

	srv.handleJoin(alice, irc.MustParse("JOIN #test"))
	if got := aliceConn.String(); !strings.Contains(got, " JOIN #test * Alice") || strings.Contains(got, " 353 ") || !strings.Contains(got, "@time=") {
		t.Fatalf("extended JOIN/no implicit NAMES/server-time: %q", got)
	}
	srv.handleJoin(bob, irc.MustParse("JOIN #test"))
	if got := bobConn.String(); strings.Contains(got, " JOIN #test * Bob") || !strings.Contains(got, "alice!a@") {
		t.Fatalf("legacy JOIN/userhost-in-names: %q", got)
	}

	aliceConn.Reset()
	srv.handleChannelMode(alice, irc.MustParse("MODE #test +v bob"), "#test")
	srv.sendNames(bob, srv.channels.GetOrCreate("#test"))
	if got := bobConn.String(); !strings.Contains(got, "+bob!b@") {
		t.Fatalf("multi-prefix membership state: %q", got)
	}
}

func TestAwayNotifyDeduplicatesSharedPeers(t *testing.T) {
	srv := New(Config{Name: "irc.test"})
	alice, bobConn := newSession(&recordingConn{}, srv), &recordingConn{}
	bob := newSession(bobConn, srv)
	alice.nick, alice.user, alice.state = "alice", "a", StateRegistered
	bob.nick, bob.user, bob.state = "bob", "b", StateRegistered
	bob.caps[irc.CapAwayNotify] = true
	for _, name := range []string{"#one", "#two"} {
		ch := srv.channels.GetOrCreate(name)
		ch.AddMember(alice, "@")
		ch.AddMember(bob, "")
	}

	srv.handleAway(alice, irc.MustParse("AWAY :gone"))
	if got := strings.Count(bobConn.String(), " AWAY gone\r\n"); got != 1 {
		t.Fatalf("AWAY notifications = %d, output %q", got, bobConn.String())
	}
}
