// Package server implements a full-featured IRC server (ircd) with RFC 2812
// and IRCv3 support.
package server

import (
	"bufio"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/natalie-o-perret/go-protocols/irc/irc"
	"github.com/natalie-o-perret/go-protocols/irc/server/mode"
	"golang.org/x/crypto/bcrypt"
)

// ---------------------------------------------------------------------------
// Session — a connected client
// ---------------------------------------------------------------------------

// SessionState tracks where a client is in the registration handshake.
type SessionState int

const (
	StatePreReg     SessionState = iota // NICK/USER not yet received
	StateCapNeg                         // CAP negotiation in progress
	StateRegistered                     // fully registered (001 sent)
)

// Membership represents a user's presence in a channel with a prefix.
type Membership struct {
	Session *Session
	Prefix  string // e.g. "@", "+", "~", "&", "%", ""
}

// Session is a single connected IRC client managed by the server.
type Session struct {
	conn       net.Conn
	writer     *bufio.Writer
	server     *Server
	writerMu   sync.Mutex
	historyMu  sync.RWMutex
	identityMu sync.RWMutex
	writeMu    sync.Mutex
	writeQueue []outbound
	writeBytes int
	writeWake  chan struct{}
	writerDone chan struct{}
	stopWriter chan struct{}
	asyncWrite bool

	// Registration fields
	nick       string
	user       string
	host       string
	realname   string
	account    string
	away       string
	secure     bool
	serverName string
	modes      *mode.Set
	caps       map[string]bool
	history    map[string][]*historyEntry
	state      SessionState
	monitorMu  sync.RWMutex
	monitor    map[string]string

	// CAP negotiation
	capVersion        int // 302 or 0
	saslActive        bool
	saslBuffer        string
	responseLabel     string
	response          []*irc.Message
	responseImmediate []*irc.Message
	captureImmediate  bool

	// Timing
	idleAt   time.Time
	signOnAt time.Time

	// Oper flag
	isOper bool
}

type outbound struct {
	line string
	done chan struct{}
}

const maxQueuedOutput = 16 << 20

type responseState struct {
	label     string
	messages  []*irc.Message
	immediate []*irc.Message
}

func newSession(conn net.Conn, srv *Server) *Session {
	host, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	return &Session{
		conn:       conn,
		writer:     bufio.NewWriterSize(conn, 4096),
		server:     srv,
		host:       host,
		modes:      mode.New(),
		caps:       make(map[string]bool),
		history:    make(map[string][]*historyEntry),
		monitor:    make(map[string]string),
		writeWake:  make(chan struct{}, 1),
		writerDone: make(chan struct{}),
		stopWriter: make(chan struct{}),
		state:      StatePreReg,
		idleAt:     time.Now(),
		signOnAt:   time.Now(),
	}
}

func (s *Session) appendHistory(target string, at time.Time, msg *irc.Message) {
	key := strings.ToLower(target)
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	s.history[key] = append(s.history[key], &historyEntry{time: at, msg: msg.Clone()})
	if len(s.history[key]) > chatHistoryLimit {
		s.history[key] = s.history[key][len(s.history[key])-chatHistoryLimit:]
	}
}

func (s *Session) historySnapshot(target string) []*historyEntry {
	s.historyMu.RLock()
	defer s.historyMu.RUnlock()
	return append([]*historyEntry(nil), s.history[strings.ToLower(target)]...)
}

func (s *Session) historyTargets() map[string][]*historyEntry {
	s.historyMu.RLock()
	defer s.historyMu.RUnlock()
	out := make(map[string][]*historyEntry, len(s.history))
	for target, entries := range s.history {
		out[target] = append([]*historyEntry(nil), entries...)
	}
	return out
}

// Prefix returns the full nick!user@host prefix for this session.
func (s *Session) Prefix() *irc.Prefix {
	s.identityMu.RLock()
	defer s.identityMu.RUnlock()
	return &irc.Prefix{Nick: s.nick, User: s.user, Host: s.host}
}

func (s *Session) identity() (account, host string) {
	s.identityMu.RLock()
	defer s.identityMu.RUnlock()
	return s.account, s.host
}

// Send writes a message to this session.
func (s *Session) Send(msg *irc.Message) {
	if msg.Command == irc.TAGMSG && !s.capEnabled(irc.CapMessageTags) {
		return
	}
	s.writerMu.Lock()
	defer s.writerMu.Unlock()
	out := msg
	if len(msg.Tags) > 0 || s.capEnabled(irc.CapServerTime) || s.capEnabled(irc.CapAccountTag) {
		out = msg.Clone()
		if s.capEnabled(irc.CapServerTime) {
			if out.Tags == nil {
				out.Tags = make(irc.Tags)
			}
			if _, ok := out.Tags["time"]; !ok {
				out.Tags["time"] = serverTime(timeNow())
			}
		}
		if s.capEnabled(irc.CapAccountTag) && out.Prefix != nil && out.Prefix.User != "" {
			if source, ok := s.server.sessions.Get(out.Prefix.Nick); ok {
				account, _ := source.identity()
				if account != "" {
					if out.Tags == nil {
						out.Tags = make(irc.Tags)
					}
					if _, exists := out.Tags["account"]; !exists {
						out.Tags["account"] = account
					}
				}
			}
		}
		for tag := range out.Tags {
			cap := irc.CapMessageTags
			switch tag {
			case "time":
				cap = irc.CapServerTime
			case "batch":
				cap = irc.CapBatch
			case "account":
				cap = irc.CapAccountTag
			case "label":
				cap = irc.CapLabeledResponse
			case "draft/chathistory-end":
				cap = irc.CapChatHistory
			}
			if !s.capEnabled(cap) {
				delete(out.Tags, tag)
			}
		}
	}
	line := out.String() + "\r\n"
	if s.response != nil {
		if s.captureImmediate {
			s.responseImmediate = append(s.responseImmediate, out.Clone())
		} else {
			s.response = append(s.response, out.Clone())
		}
		return
	}
	if s.asyncWrite {
		s.enqueue(outbound{line: line})
		return
	}
	_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = s.conn.SetWriteDeadline(time.Time{}) }()
	if _, err := s.writer.WriteString(line); err != nil {
		_ = s.conn.Close()
		return
	}
	if err := s.writer.Flush(); err != nil {
		_ = s.conn.Close()
	}
}

func (s *Session) sendImmediate(msg *irc.Message) {
	s.captureImmediate = true
	s.Send(msg)
	s.captureImmediate = false
}

func (s *Session) beginResponse(label string) {
	s.responseLabel = label
	s.response = make([]*irc.Message, 0, 1)
	s.responseImmediate = nil
}

func (s *Session) suspendResponse() responseState {
	state := responseState{label: s.responseLabel, messages: s.response, immediate: s.responseImmediate}
	s.responseLabel, s.response, s.responseImmediate = "", nil, nil
	return state
}

func (s *Session) resumeResponse(state responseState) {
	s.responseLabel, s.response, s.responseImmediate = state.label, state.messages, state.immediate
}

func (s *Session) finishResponse() []*irc.Message {
	label, messages := s.responseLabel, s.response
	immediate := s.responseImmediate
	s.responseLabel, s.response = "", nil
	s.responseImmediate = nil
	if len(messages) == 0 {
		return append(immediate, s.responseMessage(&irc.Message{Tags: irc.Tags{"label": label}, Prefix: &irc.Prefix{Nick: s.server.cfg.Name}, Command: irc.ACK}))
	}
	if len(messages) == 1 {
		messages[0].Tags = cloneTags(messages[0].Tags)
		messages[0].Tags["label"] = label
		return append(immediate, messages[0])
	}
	if messages[0].Command == irc.BATCH && strings.HasPrefix(messages[0].Param(0), "+") {
		messages[0].Tags = cloneTags(messages[0].Tags)
		messages[0].Tags["label"] = label
		return append(immediate, messages...)
	}
	batchID := rand.Text()
	out := append(immediate, s.responseMessage(&irc.Message{Tags: irc.Tags{"label": label}, Prefix: &irc.Prefix{Nick: s.server.cfg.Name}, Command: irc.BATCH, Params: []string{"+" + batchID, "labeled-response"}}))
	for _, msg := range messages {
		msg.Tags = cloneTags(msg.Tags)
		msg.Tags["batch"] = batchID
		out = append(out, msg)
	}
	return append(out, s.responseMessage(&irc.Message{Prefix: &irc.Prefix{Nick: s.server.cfg.Name}, Command: irc.BATCH, Params: []string{"-" + batchID}}))
}

func (s *Session) responseMessage(msg *irc.Message) *irc.Message {
	if !s.capEnabled(irc.CapServerTime) {
		return msg
	}
	out := msg.Clone()
	if out.Tags == nil {
		out.Tags = make(irc.Tags)
	}
	out.Tags["time"] = serverTime(timeNow())
	return out
}

func (s *Session) sendPrepared(messages []*irc.Message) {
	s.writerMu.Lock()
	defer s.writerMu.Unlock()
	if s.asyncWrite {
		for _, msg := range messages {
			s.enqueue(outbound{line: msg.String() + "\r\n"})
		}
		return
	}
	_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = s.conn.SetWriteDeadline(time.Time{}) }()
	for _, msg := range messages {
		if _, err := s.writer.WriteString(msg.String() + "\r\n"); err != nil {
			_ = s.conn.Close()
			return
		}
	}
	if err := s.writer.Flush(); err != nil {
		_ = s.conn.Close()
	}
}

func (s *Session) writerLoop() {
	defer close(s.writerDone)
	for {
		select {
		case <-s.writeWake:
			for {
				s.writeMu.Lock()
				if len(s.writeQueue) == 0 {
					s.writeMu.Unlock()
					break
				}
				item := s.writeQueue[0]
				s.writeQueue = s.writeQueue[1:]
				s.writeBytes -= len(item.line)
				s.writeMu.Unlock()
				if item.line != "" {
					_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if _, err := s.writer.WriteString(item.line); err != nil {
						_ = s.conn.Close()
						return
					}
					if err := s.writer.Flush(); err != nil {
						_ = s.conn.Close()
						return
					}
					_ = s.conn.SetWriteDeadline(time.Time{})
				}
				if item.done != nil {
					close(item.done)
				}
			}
		case <-s.stopWriter:
			return
		}
	}
}

func (s *Session) enqueue(item outbound) {
	s.writeMu.Lock()
	if s.writeBytes+len(item.line) > maxQueuedOutput {
		s.writeMu.Unlock()
		if item.done != nil {
			close(item.done)
		}
		_ = s.conn.Close()
		return
	}
	s.writeQueue = append(s.writeQueue, item)
	s.writeBytes += len(item.line)
	s.writeMu.Unlock()
	select {
	case s.writeWake <- struct{}{}:
	default:
	}
}

func (s *Session) flush() {
	if !s.asyncWrite {
		return
	}
	done := make(chan struct{})
	s.enqueue(outbound{done: done})
	select {
	case <-done:
	case <-s.writerDone:
	}
}

func cloneTags(tags irc.Tags) irc.Tags {
	out := make(irc.Tags, len(tags)+1)
	for key, value := range tags {
		out[key] = value
	}
	return out
}

// Sendf builds and sends a simple message.
func (s *Session) Sendf(cmd string, params ...string) {
	s.Send(&irc.Message{Command: cmd, Params: params})
}

// SendNumeric sends an IRC numeric reply.
func (s *Session) SendNumeric(n irc.Numeric, params ...string) {
	nick := s.nick
	if nick == "" {
		nick = "*"
	}
	all := append([]string{nick}, params...)
	s.Send(&irc.Message{
		Prefix:  &irc.Prefix{Nick: s.server.cfg.Name},
		Command: n.String(),
		Params:  all,
	})
}

// capEnabled returns true if the session has negotiated a cap.
func (s *Session) capEnabled(cap string) bool {
	return s.caps[cap]
}

// mask returns nick!user@host.
func (s *Session) mask() string {
	return s.Prefix().String()
}

// ---------------------------------------------------------------------------
// Registry — nick and channel lookup
// ---------------------------------------------------------------------------

// Registry maps lowercase nicks to sessions.
type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func newRegistry() *Registry {
	return &Registry{sessions: make(map[string]*Session)}
}

func (r *Registry) Add(s *Session) error {
	key := strings.ToLower(s.nick)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[key]; exists {
		return fmt.Errorf("nick %q already in use", s.nick)
	}
	r.sessions[key] = s
	return nil
}

func (r *Registry) Remove(nick string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, strings.ToLower(nick))
}

func (r *Registry) Rename(oldNick, newNick string) error {
	oldKey := strings.ToLower(oldNick)
	newKey := strings.ToLower(newNick)
	r.mu.Lock()
	defer r.mu.Unlock()
	if oldKey == newKey {
		return nil
	}
	s, ok := r.sessions[oldKey]
	if !ok {
		return fmt.Errorf("nick %q not found", oldNick)
	}
	if _, exists := r.sessions[newKey]; exists {
		return fmt.Errorf("nick %q already in use", newNick)
	}
	delete(r.sessions, oldKey)
	r.sessions[newKey] = s
	return nil
}

func (s *Session) isMonitoring(nick string) bool {
	s.monitorMu.RLock()
	defer s.monitorMu.RUnlock()
	_, ok := s.monitor[strings.ToLower(nick)]
	return ok
}

func (s *Session) monitorSnapshot() []string {
	s.monitorMu.RLock()
	defer s.monitorMu.RUnlock()
	out := make([]string, 0, len(s.monitor))
	for _, nick := range s.monitor {
		out = append(out, nick)
	}
	return out
}

func (r *Registry) Get(nick string) (*Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[strings.ToLower(nick)]
	return s, ok
}

func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

func (r *Registry) All() []*Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}
	return out
}

// ---------------------------------------------------------------------------
// Channel
// ---------------------------------------------------------------------------

// Channel represents an IRC channel.
type Channel struct {
	name       string
	topic      string
	topicSetBy string
	topicSetAt time.Time
	modes      *mode.Set
	createdAt  time.Time

	mu      sync.RWMutex
	members map[string]*Membership // lowercase nick -> Membership
	history []*historyEntry
}

type historyEntry struct {
	time time.Time
	msg  *irc.Message
}

func newChannel(name string) *Channel {
	return &Channel{
		name:      name,
		modes:     mode.New(),
		createdAt: time.Now(),
		members:   make(map[string]*Membership),
	}
}

func (ch *Channel) appendHistory(at time.Time, msg *irc.Message) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.history = append(ch.history, &historyEntry{time: at, msg: msg.Clone()})
	if len(ch.history) > chatHistoryLimit {
		ch.history = ch.history[len(ch.history)-chatHistoryLimit:]
	}
}

func (ch *Channel) historySnapshot() []*historyEntry {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	return append([]*historyEntry(nil), ch.history...)
}

// AddMember adds a session to the channel.
func (ch *Channel) AddMember(s *Session, prefix string) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.members[strings.ToLower(s.nick)] = &Membership{Session: s, Prefix: prefix}
}

// RemoveMember removes a session from the channel.  Returns true if the channel is now empty.
func (ch *Channel) RemoveMember(nick string) bool {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	delete(ch.members, strings.ToLower(nick))
	return len(ch.members) == 0
}

// HasMember returns true if nick is in the channel.
func (ch *Channel) HasMember(nick string) bool {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	_, ok := ch.members[strings.ToLower(nick)]
	return ok
}

// GetMembership returns the Membership for a nick, or nil.
func (ch *Channel) GetMembership(nick string) *Membership {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	membership := ch.members[strings.ToLower(nick)]
	if membership == nil {
		return nil
	}
	copy := *membership
	return &copy
}

// Members returns a snapshot of all memberships.
func (ch *Channel) Members() []*Membership {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	out := make([]*Membership, 0, len(ch.members))
	for _, m := range ch.members {
		copy := *m
		out = append(out, &copy)
	}
	return out
}

// Broadcast sends a message to all members, optionally excluding one session.
func (ch *Channel) Broadcast(msg *irc.Message, exclude *Session) {
	ch.mu.RLock()
	members := make([]*Membership, 0, len(ch.members))
	for _, m := range ch.members {
		members = append(members, m)
	}
	ch.mu.RUnlock()

	for _, m := range members {
		if m.Session == exclude {
			continue
		}
		m.Session.Send(msg)
	}
}

// NamesReply returns the NAMES list for this channel, honouring multi-prefix.
func (ch *Channel) NamesReply(multiPrefix bool) []string {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	names := make([]string, 0, len(ch.members))
	for _, m := range ch.members {
		prefix := m.Prefix
		if !multiPrefix && len(prefix) > 1 {
			prefix = string(prefix[0])
		}
		names = append(names, prefix+m.Session.nick)
	}
	return names
}

func (ch *Channel) applyMembershipMode(change irc.ModeChange) bool {
	index := strings.IndexRune("qaohv", change.Mode)
	if index < 0 || change.Arg == "" {
		return false
	}
	prefix := "~&@%+"[index]
	ch.mu.Lock()
	defer ch.mu.Unlock()
	membership := ch.members[strings.ToLower(change.Arg)]
	if membership == nil {
		return false
	}
	current := strings.ReplaceAll(membership.Prefix, string(prefix), "")
	if change.Add {
		current += string(prefix)
	}
	var ordered strings.Builder
	for _, candidate := range "~&@%+" {
		if strings.ContainsRune(current, candidate) {
			ordered.WriteRune(candidate)
		}
	}
	if membership.Prefix == ordered.String() {
		return false
	}
	membership.Prefix = ordered.String()
	return true
}

// ChannelRegistry maps channel names to Channel objects.
type ChannelRegistry struct {
	mu       sync.RWMutex
	channels map[string]*Channel
}

func newChannelRegistry() *ChannelRegistry {
	return &ChannelRegistry{channels: make(map[string]*Channel)}
}

func (cr *ChannelRegistry) GetOrCreate(name string) *Channel {
	key := strings.ToLower(name)
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if ch, ok := cr.channels[key]; ok {
		return ch
	}
	ch := newChannel(name)
	cr.channels[key] = ch
	return ch
}

func (cr *ChannelRegistry) Get(name string) (*Channel, bool) {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	ch, ok := cr.channels[strings.ToLower(name)]
	return ch, ok
}

func (cr *ChannelRegistry) Remove(name string) {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	delete(cr.channels, strings.ToLower(name))
}

func (cr *ChannelRegistry) All() []*Channel {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	out := make([]*Channel, 0, len(cr.channels))
	for _, ch := range cr.channels {
		out = append(out, ch)
	}
	return out
}

func (cr *ChannelRegistry) Count() int {
	cr.mu.RLock()
	defer cr.mu.RUnlock()
	return len(cr.channels)
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

// Config holds the server configuration.
type Config struct {
	// Name is the server hostname used in messages.
	Name string
	// Network is the IRC network name reported in RPL_ISUPPORT.
	Network string
	// Listen is the address to listen on (e.g. ":6667").
	Listen string
	// TLSListen is an optional TLS listener address (e.g. ":6697").
	TLSListen string
	// TLSConfig is required if TLSListen is set.
	TLSConfig *tls.Config
	// MOTD is the message-of-the-day text (may contain \n lines).
	MOTD string
	// MaxClients is the maximum number of simultaneous connections (0 = unlimited).
	MaxClients int
	// Password is a global server password (optional).
	Password string
	// Opers maps oper name to bcrypt hashed password.
	Opers map[string]string
	// Accounts maps case-insensitive account names to SASL credentials.
	Accounts map[string]Account
	// STS is the optional strict transport security policy.
	STS *STSConfig
	// Caps is retained for configuration compatibility.
	// Deprecated: capabilities without implementations are ignored.
	Caps []string
	// PingInterval is how often to ping idle clients.
	PingInterval time.Duration
	// PingTimeout is disconnect timeout after no PONG.
	PingTimeout time.Duration
}

// Account is a configured SASL account.
type Account struct {
	Name         string
	PasswordHash string
	Host         string
}

// STSConfig controls strict transport security advertisement.
type STSConfig struct {
	Port      int
	Duration  time.Duration
	Preload   bool
	Hostnames []string
}

func (c *Config) setDefaults() {
	if c.Name == "" {
		c.Name = "irc.local"
	}
	if c.Network == "" {
		c.Network = "LocalNet"
	}
	if c.Listen == "" {
		c.Listen = ":6667"
	}
	if c.PingInterval <= 0 {
		c.PingInterval = 90 * time.Second
	}
	if c.PingTimeout <= 0 {
		c.PingTimeout = 30 * time.Second
	}
}

// Server is the IRC server.
type Server struct {
	cfg               Config
	sessions          *Registry
	channels          *ChannelRegistry
	log               *slog.Logger
	accounts          map[string]Account
	dummyPasswordHash string

	// Stats
	clientsTotal   atomic.Int64
	clientsCurrent atomic.Int64

	// Listeners (for graceful shutdown)
	listenerMu sync.Mutex
	listeners  []net.Listener
	dispatchMu sync.RWMutex

	// WhoWas ring buffer (simple slice for now)
	whowasMu sync.Mutex
	whowas   []whowasEntry
}

type whowasEntry struct {
	nick     string
	user     string
	host     string
	realname string
	quitAt   time.Time
}

// New creates a new Server with the given config.
func New(cfg Config) *Server {
	cfg.setDefaults()
	accounts := make(map[string]Account, len(cfg.Accounts))
	passwordCost := bcrypt.DefaultCost
	for name, account := range cfg.Accounts {
		if account.Name == "" {
			account.Name = name
		}
		accounts[strings.ToLower(name)] = account
		if cost, err := bcrypt.Cost([]byte(account.PasswordHash)); err == nil {
			passwordCost = cost
		}
	}
	var dummyHash []byte
	if len(accounts) > 0 {
		dummyHash, _ = bcrypt.GenerateFromPassword([]byte("invalid"), passwordCost)
	}
	return &Server{
		cfg:               cfg,
		sessions:          newRegistry(),
		channels:          newChannelRegistry(),
		log:               slog.Default(),
		accounts:          accounts,
		dummyPasswordHash: string(dummyHash),
		whowas:            make([]whowasEntry, 0, 64),
	}
}

// ListenAndServe starts the server listeners.
func (srv *Server) ListenAndServe() error {
	if err := srv.validateConfig(); err != nil {
		return err
	}
	errCh := make(chan error, 2)

	ln, err := net.Listen("tcp", srv.cfg.Listen)
	if err != nil {
		return fmt.Errorf("server: listen %s: %w", srv.cfg.Listen, err)
	}
	srv.trackListener(ln)
	srv.log.Info("IRC server listening", "addr", srv.cfg.Listen)
	go func() { errCh <- srv.acceptLoop(ln, false) }()

	if srv.cfg.TLSListen != "" && srv.cfg.TLSConfig != nil {
		tlsLn, err := tls.Listen("tcp", srv.cfg.TLSListen, srv.cfg.TLSConfig)
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("server: tls listen %s: %w", srv.cfg.TLSListen, err)
		}
		srv.trackListener(tlsLn)
		srv.log.Info("IRC/TLS server listening", "addr", srv.cfg.TLSListen)
		go func() { errCh <- srv.acceptLoop(tlsLn, true) }()
	}

	return <-errCh
}

// ListenRandom binds to a random port on the configured listen address
// (or 127.0.0.1:0 if Listen is empty) and returns the actual address.
// Call ServeListener to start accepting connections.
func (srv *Server) ListenRandom() (string, error) {
	if err := srv.validateConfig(); err != nil {
		return "", err
	}
	listen := srv.cfg.Listen
	if listen == "" {
		listen = "127.0.0.1:0"
	}
	// Replace :0 or keep as-is if it already has a specific port
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return "", fmt.Errorf("server: listen: %w", err)
	}
	srv.trackListener(ln)
	srv.log.Info("IRC server listening", "addr", ln.Addr())
	return ln.Addr().String(), nil
}

// ServeListener starts accepting on all tracked listeners in the background.
func (srv *Server) ServeListener() {
	srv.listenerMu.Lock()
	lns := append([]net.Listener(nil), srv.listeners...)
	srv.listenerMu.Unlock()
	for _, ln := range lns {
		go srv.acceptLoop(ln, false) //nolint:errcheck
	}
}

// Close shuts down all listeners.
func (srv *Server) Close() {
	srv.listenerMu.Lock()
	defer srv.listenerMu.Unlock()
	for _, ln := range srv.listeners {
		_ = ln.Close()
	}
}

func (srv *Server) trackListener(ln net.Listener) {
	srv.listenerMu.Lock()
	srv.listeners = append(srv.listeners, ln)
	srv.listenerMu.Unlock()
}

func (srv *Server) acceptLoop(ln net.Listener, secure bool) error {
	defer func() { _ = ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		current := srv.clientsCurrent.Add(1)
		if srv.cfg.MaxClients > 0 && int(current) > srv.cfg.MaxClients {
			srv.clientsCurrent.Add(-1)
			_, _ = conn.Write([]byte("ERROR :Server is full\r\n"))
			_ = conn.Close()
			continue
		}
		go srv.startSession(conn, secure)
	}
}

func (srv *Server) startSession(conn net.Conn, secure bool) {
	if tlsConn, ok := conn.(*tls.Conn); ok {
		_ = tlsConn.SetDeadline(time.Now().Add(10 * time.Second))
		if err := tlsConn.Handshake(); err != nil {
			_ = conn.Close()
			srv.clientsCurrent.Add(-1)
			return
		}
		_ = tlsConn.SetDeadline(time.Time{})
	}
	s := newSession(conn, srv)
	s.secure = secure
	if tlsConn, ok := conn.(*tls.Conn); ok {
		s.serverName = tlsConn.ConnectionState().ServerName
	}
	if secure {
		s.modes.Apply([]irc.ModeChange{{Add: true, Mode: 'z'}}, mode.UserValidator{})
	}
	srv.clientsTotal.Add(1)
	srv.handleSession(s)
}

func (srv *Server) validateConfig() error {
	if srv.cfg.TLSListen != "" && srv.cfg.TLSConfig == nil {
		return fmt.Errorf("server: TLSListen requires TLSConfig")
	}
	passwordCost := -1
	for name, account := range srv.accounts {
		cost, err := bcrypt.Cost([]byte(account.PasswordHash))
		if err != nil {
			return fmt.Errorf("server: invalid password hash for account %q", name)
		}
		if passwordCost < 0 {
			passwordCost = cost
		} else if cost != passwordCost {
			return fmt.Errorf("server: account password hashes must use the same bcrypt cost")
		}
	}
	if srv.cfg.STS == nil {
		return nil
	}
	sts := srv.cfg.STS
	if sts.Port < 1 || sts.Port > 65535 || sts.Duration < 0 || sts.Preload && sts.Duration == 0 {
		return fmt.Errorf("server: invalid STS policy")
	}
	if srv.cfg.TLSListen == "" || srv.cfg.TLSConfig == nil || len(sts.Hostnames) == 0 {
		return fmt.Errorf("server: STS requires TLS and at least one hostname")
	}
	return nil
}

func (srv *Server) handleSession(s *Session) {
	s.asyncWrite = true
	go s.writerLoop()
	defer func() {
		srv.clientsCurrent.Add(-1)
		srv.dispatchMu.RLock()
		srv.removeSession(s)
		srv.dispatchMu.RUnlock()
		close(s.stopWriter)
		<-s.writerDone
		_ = s.conn.Close()
	}()

	scanner := bufio.NewScanner(s.conn)
	scanner.Buffer(make([]byte, 8192+4096), 8192+4096)

	for scanner.Scan() {
		line := scanner.Text()
		if irc.InputTooLong(line) {
			srv.dispatchMu.RLock()
			s.SendNumeric(irc.ERR_INPUTTOOLONG, "Input line was too long")
			srv.dispatchMu.RUnlock()
			continue
		}
		msg, err := irc.Parse(line)
		if err != nil {
			continue
		}
		s.idleAt = time.Now()
		srv.dispatchCommand(s, msg)
	}
}

func (srv *Server) removeSession(s *Session) {
	if s.nick != "" {
		// Record in whowas
		srv.whowasMu.Lock()
		_, host := s.identity()
		srv.whowas = append(srv.whowas, whowasEntry{
			nick:     s.nick,
			user:     s.user,
			host:     host,
			realname: s.realname,
			quitAt:   time.Now(),
		})
		if len(srv.whowas) > 256 {
			srv.whowas = srv.whowas[1:]
		}
		srv.whowasMu.Unlock()

		// Part all channels
		for _, ch := range srv.channels.All() {
			if !ch.HasMember(s.nick) {
				continue
			}
			ch.Broadcast(&irc.Message{
				Prefix:  s.Prefix(),
				Command: irc.QUIT,
				Params:  []string{"Connection closed"},
			}, s)
			empty := ch.RemoveMember(s.nick)
			if empty {
				srv.channels.Remove(ch.name)
			}
		}
		srv.sessions.Remove(s.nick)
		srv.notifyMonitorOffline(s.nick)
	}
}

// Broadcast sends a message to all registered sessions.
func (srv *Server) Broadcast(msg *irc.Message) {
	srv.dispatchMu.RLock()
	defer srv.dispatchMu.RUnlock()
	for _, s := range srv.sessions.All() {
		s.Send(msg)
	}
}

// isupport returns the 005 ISUPPORT tokens.
func (srv *Server) isupport() []string {
	return []string{
		"NETWORK=" + srv.cfg.Network,
		"CASEMAPPING=ascii",
		"CHANMODES=beIq,k,l,imnpstrcCzGSNufjQKLPOARTF",
		"CHANTYPES=#&",
		"PREFIX=(qaohv)~&@%+",
		"STATUSMSG=@+",
		"MODES=4",
		"MAXCHANNELS=25",
		"MAXNICKLEN=30",
		"MAXBANS=100",
		"TOPICLEN=390",
		"KICKLEN=255",
		"AWAYLEN=307",
		"CHANNELLEN=64",
		"EXTBAN=$,ajoqrz",
		"WHOX",
		"WALLCHOPS",
		"ELIST=MNUCT",
		"INVEX",
		"EXCEPTS",
		"CALLERID",
		"MONITOR=100",
		"CHATHISTORY=50",
		"MSGREFTYPES=timestamp,msgid",
	}
}

// supportedCaps returns the set of caps this server supports.
func (srv *Server) supportedCaps(s *Session) map[string]string {
	caps := map[string]string{
		irc.CapServerTime:      "",
		irc.CapMessageTags:     "",
		irc.CapBatch:           "",
		irc.CapChatHistory:     "",
		irc.CapEchoMessage:     "",
		irc.CapMultiPrefix:     "",
		irc.CapAwayNotify:      "",
		irc.CapExtendedJoin:    "",
		irc.CapSetname:         "",
		irc.CapCapNotify:       "",
		irc.CapInviteNotify:    "",
		irc.CapUserHostInNames: "",
		irc.CapNoImplicitNames: "",
		irc.CapStandardReplies: "",
		irc.CapAccountTag:      "",
		irc.CapAccountNotify:   "",
		irc.CapChghost:         "",
		irc.CapExtendedMonitor: "",
		irc.CapLabeledResponse: "",
	}
	if s != nil && s.secure && len(srv.accounts) > 0 {
		caps[irc.CapSASL] = "PLAIN"
	}
	if s != nil && s.capVersion >= 302 && srv.cfg.STS != nil {
		if !s.secure {
			caps[irc.CapSTS] = fmt.Sprintf("port=%d", srv.cfg.STS.Port)
		} else if slicesContainsFold(srv.cfg.STS.Hostnames, s.serverName) {
			value := fmt.Sprintf("duration=%d", int64(srv.cfg.STS.Duration/time.Second))
			if srv.cfg.STS.Preload {
				value += ",preload"
			}
			caps[irc.CapSTS] = value
		}
	}
	return caps
}

func slicesContainsFold(values []string, value string) bool {
	for _, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return true
		}
	}
	return false
}

// serverTime returns the current time in IRCv3 server-time format.
const chatHistoryLimit = 50

func serverTime(t time.Time) string { return t.UTC().Format(irc.ServerTimeLayout) }
