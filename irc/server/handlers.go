package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/natalie-o-perret/go-protocols/irc/irc"
	"github.com/natalie-o-perret/go-protocols/irc/server/mode"
)

func (srv *Server) dispatchCommand(s *Session, msg *irc.Message) {
	label, ok := msg.Tags.Get("label")
	if !ok || label == "" || len(label) > 64 || msg.Command == irc.QUIT || !s.capEnabled(irc.CapLabeledResponse) || !s.capEnabled(irc.CapBatch) {
		srv.dispatchMu.RLock()
		srv.dispatch(s, msg)
		srv.dispatchMu.RUnlock()
		return
	}
	srv.dispatchMu.Lock()
	s.beginResponse(label)
	srv.dispatch(s, msg)
	response := s.finishResponse()
	srv.dispatchMu.Unlock()
	s.sendPrepared(response)
}

// dispatch routes a message to the appropriate handler.
func (srv *Server) dispatch(s *Session, msg *irc.Message) {
	cmd := strings.ToUpper(msg.Command)

	// Always allow these before registration
	switch cmd {
	case irc.CAP:
		srv.handleCAP(s, msg)
		return
	case irc.PASS:
		srv.handlePass(s, msg)
		return
	case irc.NICK:
		srv.handleNick(s, msg)
		return
	case irc.USER:
		srv.handleUser(s, msg)
		return
	case irc.QUIT:
		srv.handleQuit(s, msg)
		return
	case irc.PING:
		srv.handlePing(s, msg)
		return
	case irc.PONG:
		return // no-op
	case irc.AUTHENTICATE:
		srv.handleAuthenticate(s, msg)
		return
	}

	// Require registration for everything else
	if s.state != StateRegistered {
		s.SendNumeric(irc.ERR_NOTREGISTERED, "You have not registered")
		return
	}

	switch cmd {
	// Channel
	case irc.JOIN:
		srv.handleJoin(s, msg)
	case irc.PART:
		srv.handlePart(s, msg)
	case irc.TOPIC:
		srv.handleTopic(s, msg)
	case irc.NAMES:
		srv.handleNames(s, msg)
	case irc.LIST:
		srv.handleList(s, msg)
	case irc.KICK:
		srv.handleKick(s, msg)
	case irc.INVITE:
		srv.handleInvite(s, msg)
	case irc.MODE:
		srv.handleMode(s, msg)

	// Messaging
	case irc.PRIVMSG:
		srv.handlePrivmsg(s, msg, false)
	case irc.NOTICE:
		srv.handlePrivmsg(s, msg, true)
	case irc.TAGMSG:
		srv.handleTagmsg(s, msg)
	case irc.CHATHISTORY:
		srv.handleChatHistory(s, msg)

	// Queries
	case irc.WHO:
		srv.handleWho(s, msg)
	case irc.WHOIS:
		srv.handleWhois(s, msg)
	case irc.WHOWAS:
		srv.handleWhowas(s, msg)

	// User state
	case irc.AWAY:
		srv.handleAway(s, msg)
	case irc.NICK:
		srv.handleNickRegistered(s, msg)
	case irc.USERHOST:
		srv.handleUserhost(s, msg)
	case irc.ISON:
		srv.handleIson(s, msg)
	case irc.MONITOR:
		srv.handleMonitor(s, msg)

	// Server
	case irc.PING:
		srv.handlePing(s, msg)
	case irc.MOTD:
		srv.sendMOTD(s)
	case irc.LUSERS:
		srv.sendLusers(s)
	case irc.VERSION:
		srv.handleVersion(s, msg)
	case irc.TIME:
		srv.handleTime(s, msg)
	case irc.ADMIN:
		srv.handleAdmin(s, msg)
	case irc.INFO:
		srv.handleInfo(s, msg)
	case irc.STATS:
		srv.handleStats(s, msg)
	case irc.WALLOPS:
		srv.handleWallops(s, msg)

	// Oper
	case irc.OPER:
		srv.handleOper(s, msg)
	case irc.KILL:
		srv.handleKill(s, msg)

	// IRCv3
	case irc.SETNAME:
		srv.handleSetname(s, msg)

	default:
		s.SendNumeric(irc.ERR_UNKNOWNCOMMAND, cmd, "Unknown command")
	}
}

// ---------------------------------------------------------------------------
// Registration handlers
// ---------------------------------------------------------------------------

func (srv *Server) handlePass(s *Session, msg *irc.Message) {
	if s.state == StateRegistered {
		s.SendNumeric(irc.ERR_ALREADYREGISTRED, "You may not reregister")
		return
	}
	// Password is validated at registration completion
}

func (srv *Server) handleNick(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NONICKNAMEGIVEN, "No nickname given")
		return
	}
	nick := msg.Params[0]
	if !isValidNick(nick) {
		s.SendNumeric(irc.ERR_ERRONEUSNICKNAME, nick, "Erroneous Nickname")
		return
	}
	if _, taken := srv.sessions.Get(nick); taken && !strings.EqualFold(nick, s.nick) {
		s.SendNumeric(irc.ERR_NICKNAMEINUSE, nick, "Nickname is already in use")
		return
	}
	s.nick = nick
	srv.tryRegister(s)
}

func (srv *Server) handleNickRegistered(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NONICKNAMEGIVEN, "No nickname given")
		return
	}
	newNick := msg.Params[0]
	if !isValidNick(newNick) {
		s.SendNumeric(irc.ERR_ERRONEUSNICKNAME, newNick, "Erroneous Nickname")
		return
	}
	if _, taken := srv.sessions.Get(newNick); taken && !strings.EqualFold(newNick, s.nick) {
		s.SendNumeric(irc.ERR_NICKNAMEINUSE, newNick, "Nickname is already in use")
		return
	}
	oldNick := s.nick
	oldPrefix := s.Prefix()
	if err := srv.sessions.Rename(oldNick, newNick); err != nil {
		s.SendNumeric(irc.ERR_NICKNAMEINUSE, newNick, "Nickname is already in use")
		return
	}
	s.nick = newNick
	if !strings.EqualFold(oldNick, newNick) {
		srv.notifyMonitorOffline(oldNick)
		srv.notifyMonitorOnline(s)
	}
	nickMsg := &irc.Message{
		Prefix:  oldPrefix,
		Command: irc.NICK,
		Params:  []string{newNick},
	}
	if account, _ := s.identity(); account != "" {
		nickMsg.Tags = irc.Tags{"account": account}
	}
	s.Send(nickMsg)
	// Broadcast to shared channels
	notified := map[string]bool{strings.ToLower(oldNick): true}
	for _, ch := range srv.channels.All() {
		if !ch.HasMember(oldNick) && !ch.HasMember(newNick) {
			continue
		}
		// Update membership
		ch.mu.Lock()
		if m, ok := ch.members[strings.ToLower(oldNick)]; ok {
			delete(ch.members, strings.ToLower(oldNick))
			ch.members[strings.ToLower(newNick)] = m
		}
		ch.mu.Unlock()
		for _, m := range ch.Members() {
			key := strings.ToLower(m.Session.nick)
			if !notified[key] {
				notified[key] = true
				m.Session.Send(nickMsg)
			}
		}
	}
}

func (srv *Server) handleUser(s *Session, msg *irc.Message) {
	if s.state == StateRegistered {
		s.SendNumeric(irc.ERR_ALREADYREGISTRED, "You may not reregister")
		return
	}
	if len(msg.Params) < 4 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.USER, "Not enough parameters")
		return
	}
	s.user = cleanUsername(msg.Params[0])
	s.realname = msg.Params[3]
	srv.tryRegister(s)
}

func (srv *Server) tryRegister(s *Session) {
	if s.state == StateRegistered {
		return
	}
	if s.state == StateCapNeg {
		return // wait for CAP END
	}
	if s.nick == "" || s.user == "" {
		return
	}

	if err := srv.sessions.Add(s); err != nil {
		s.SendNumeric(irc.ERR_NICKNAMEINUSE, s.nick, "Nickname is already in use")
		s.nick = ""
		return
	}

	s.state = StateRegistered
	srv.notifyMonitorOnline(s)

	// Send welcome sequence
	s.SendNumeric(irc.RPL_WELCOME, fmt.Sprintf("Welcome to the %s IRC Network %s", srv.cfg.Network, s.mask()))
	s.SendNumeric(irc.RPL_YOURHOST, fmt.Sprintf("Your host is %s, running go-irc", srv.cfg.Name))
	s.SendNumeric(irc.RPL_CREATED, "This server was created just now")
	s.SendNumeric(irc.RPL_MYINFO, srv.cfg.Name, "go-irc-1.0", "iowzBGHRSXZaegimnpstv", "beIqaohvIMRScOAQKFLPTuGzjf")

	// Send ISUPPORT in batches of 13
	tokens := srv.isupport()
	for len(tokens) > 0 {
		batch := tokens
		if len(batch) > 13 {
			batch = tokens[:13]
		}
		tokens = tokens[len(batch):]
		params := append(batch, "are supported by this server")
		s.SendNumeric(irc.RPL_ISUPPORT, params...)
	}

	srv.sendLusers(s)
	srv.sendMOTD(s)
}

// ---------------------------------------------------------------------------
// CAP
// ---------------------------------------------------------------------------

func (srv *Server) handleCAP(s *Session, msg *irc.Message) {
	if len(msg.Params) < 1 {
		s.SendNumeric(irc.ERR_INVALIDCAPCMD, "*", "Invalid CAP command")
		return
	}
	subCmd := strings.ToUpper(msg.Params[0])
	subArg := ""
	if len(msg.Params) >= 2 {
		subArg = msg.Params[1]
	}

	supported := srv.supportedCaps(s)

	switch subCmd {
	case irc.CapLS:
		if s.state != StateRegistered {
			s.state = StateCapNeg
		}
		if version, err := strconv.Atoi(subArg); err == nil && version >= 302 {
			s.capVersion = version
			s.caps[irc.CapCapNotify] = true
		}
		supported = srv.supportedCaps(s)

		var parts []string
		for k, v := range supported {
			if s.capVersion >= 302 && v != "" {
				parts = append(parts, k+"="+v)
			} else {
				parts = append(parts, k)
			}
		}
		slices.Sort(parts)
		srv.sendCAPList(s, irc.CapLS, parts)

	case irc.CapLIST:
		var enabled []string
		for cap := range s.caps {
			if s.caps[cap] {
				enabled = append(enabled, cap)
			}
		}
		slices.Sort(enabled)
		srv.sendCAPList(s, irc.CapLIST, enabled)

	case irc.CapREQ:
		if s.state != StateRegistered {
			s.state = StateCapNeg
		}
		req := strings.TrimPrefix(subArg, ":")
		for _, cap := range strings.Fields(req) {
			capName := strings.TrimPrefix(cap, "-")
			if _, ok := supported[capName]; !ok || capName == irc.CapSTS {
				srv.sendCAP(s, irc.CapNAK, req)
				return
			}
		}
		for _, cap := range strings.Fields(req) {
			disable := strings.HasPrefix(cap, "-")
			capName := strings.TrimPrefix(cap, "-")
			if disable && (capName != irc.CapCapNotify || s.capVersion < 302) {
				delete(s.caps, capName)
			} else if !disable {
				s.caps[capName] = true
			}
		}
		srv.sendCAP(s, irc.CapACK, req)

	case irc.CapEND:
		if s.saslActive {
			srv.saslFailure(s, irc.ERR_SASLABORTED, "SASL authentication aborted")
		}
		if s.state == StateCapNeg {
			s.state = StatePreReg
		}
		srv.tryRegister(s)

	default:
		s.SendNumeric(irc.ERR_INVALIDCAPCMD, subCmd, "Invalid CAP command")
	}
}

const maxSASLPayload = 4096

func (srv *Server) handleAuthenticate(s *Session, msg *irc.Message) {
	if !s.capEnabled(irc.CapSASL) || len(msg.Params) == 0 {
		srv.saslFailure(s, irc.ERR_SASLFAIL, "SASL authentication failed")
		return
	}
	chunk := msg.Params[0]
	if !s.saslActive {
		if account, _ := s.identity(); account != "" {
			s.SendNumeric(irc.ERR_SASLALREADY, "You have already authenticated using SASL")
			return
		}
		if !strings.EqualFold(chunk, "PLAIN") {
			s.SendNumeric(irc.RPL_SASLMECHS, "PLAIN", "are available SASL mechanisms")
			srv.saslFailure(s, irc.ERR_SASLFAIL, "SASL authentication failed")
			return
		}
		s.saslActive = true
		s.saslBuffer = ""
		s.Sendf(irc.AUTHENTICATE, "+")
		return
	}

	if chunk == "*" {
		srv.saslFailure(s, irc.ERR_SASLABORTED, "SASL authentication aborted")
		return
	}
	if len(chunk) > 400 || len(s.saslBuffer)+len(chunk) > maxSASLPayload {
		srv.saslFailure(s, irc.ERR_SASLTOOLONG, "SASL message too long")
		return
	}
	if chunk != "+" {
		s.saslBuffer += chunk
	}
	if len(chunk) == 400 {
		return
	}

	encoded := s.saslBuffer
	s.saslActive = false
	s.saslBuffer = ""
	payload, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		s.SendNumeric(irc.ERR_SASLFAIL, "SASL authentication failed")
		return
	}
	defer clear(payload)
	fields := strings.Split(string(payload), "\x00")
	if len(fields) != 3 || fields[1] == "" || fields[2] == "" || fields[0] != "" && !strings.EqualFold(fields[0], fields[1]) {
		s.SendNumeric(irc.ERR_SASLFAIL, "SASL authentication failed")
		return
	}
	account, ok := srv.accounts[strings.ToLower(fields[1])]
	hash := srv.dummyPasswordHash
	if ok {
		hash = account.PasswordHash
	}
	passwordOK := srv.checkPassword(s, hash, fields[2])
	if !passwordOK || !ok {
		s.SendNumeric(irc.ERR_SASLFAIL, "SASL authentication failed")
		return
	}

	oldPrefix := s.Prefix()
	s.identityMu.Lock()
	s.account = account.Name
	s.identityMu.Unlock()
	if s.state == StateRegistered {
		srv.notifyAccount(s, oldPrefix)
	}
	_, currentHost := s.identity()
	if account.Host != "" && account.Host != currentHost {
		oldHostPrefix := s.Prefix()
		s.identityMu.Lock()
		s.host = account.Host
		s.identityMu.Unlock()
		if s.state == StateRegistered {
			srv.notifyHostChange(s, oldHostPrefix)
		}
	}
	accountName, _ := s.identity()
	s.SendNumeric(irc.RPL_LOGGEDIN, s.mask(), accountName, "You are now logged in as "+accountName)
	s.SendNumeric(irc.RPL_SASLSUCCESS, "SASL authentication successful")
}

func (srv *Server) checkPassword(s *Session, hash, password string) bool {
	if s.response == nil {
		return checkBcrypt(hash, password)
	}
	response := s.suspendResponse()
	srv.dispatchMu.Unlock()
	ok := checkBcrypt(hash, password)
	srv.dispatchMu.Lock()
	s.resumeResponse(response)
	return ok
}

func (srv *Server) saslFailure(s *Session, numeric irc.Numeric, text string) {
	s.saslActive = false
	s.saslBuffer = ""
	s.SendNumeric(numeric, text)
}

func (srv *Server) notifyAccount(s *Session, prefix *irc.Prefix) {
	account, _ := s.identity()
	msg := &irc.Message{Prefix: prefix, Command: irc.ACCOUNT, Params: []string{account}}
	notified := make(map[*Session]bool)
	if s.capEnabled(irc.CapAccountNotify) {
		notified[s] = true
		s.Send(msg)
	}
	for _, ch := range srv.channels.All() {
		if !ch.HasMember(s.nick) {
			continue
		}
		for _, member := range ch.Members() {
			peer := member.Session
			if !notified[peer] && peer.capEnabled(irc.CapAccountNotify) {
				notified[peer] = true
				peer.Send(msg)
			}
		}
	}
	for _, peer := range srv.extendedMonitorPeers(s, irc.CapAccountNotify) {
		if !notified[peer] {
			notified[peer] = true
			peer.Send(msg)
		}
	}
}

func (srv *Server) notifyHostChange(s *Session, oldPrefix *irc.Prefix) {
	account, host := s.identity()
	msg := &irc.Message{Prefix: oldPrefix, Command: irc.CHGHOST, Params: []string{s.user, host}}
	notified := make(map[*Session]bool)
	if s.capEnabled(irc.CapChghost) {
		notified[s] = true
		s.Send(msg)
	}
	shared := make(map[*Session][]*Channel)
	for _, ch := range srv.channels.All() {
		if !ch.HasMember(s.nick) {
			continue
		}
		for _, member := range ch.Members() {
			if member.Session != s {
				shared[member.Session] = append(shared[member.Session], ch)
			}
		}
	}
	for peer, channels := range shared {
		if peer.capEnabled(irc.CapChghost) {
			if !notified[peer] {
				notified[peer] = true
				peer.Send(msg)
			}
			continue
		}
		peer.Send(&irc.Message{Prefix: oldPrefix, Command: irc.QUIT, Params: []string{"Changing hostname"}})
		for _, ch := range channels {
			join := &irc.Message{Prefix: s.Prefix(), Command: irc.JOIN, Params: []string{ch.name}}
			if peer.capEnabled(irc.CapExtendedJoin) {
				join.Params = append(join.Params, account, s.realname)
			}
			peer.Send(join)
			if membership := ch.GetMembership(s.nick); membership != nil {
				for i, prefix := range "~&@%+" {
					if strings.ContainsRune(membership.Prefix, prefix) {
						peer.Send(&irc.Message{Prefix: &irc.Prefix{Nick: srv.cfg.Name}, Command: irc.MODE, Params: []string{ch.name, "+" + string("qaohv"[i]), s.nick}})
					}
				}
			}
		}
	}
	for _, peer := range srv.extendedMonitorPeers(s, irc.CapChghost) {
		if !notified[peer] {
			notified[peer] = true
			peer.Send(msg)
		}
	}
}

func (srv *Server) sendCAP(s *Session, subCmd, value string) {
	nick := s.nick
	if nick == "" {
		nick = "*"
	}
	s.Send(&irc.Message{
		Prefix:  &irc.Prefix{Nick: srv.cfg.Name},
		Command: irc.CAP,
		Params:  []string{nick, subCmd, value},
	})
}

func (srv *Server) sendCAPList(s *Session, subCmd string, caps []string) {
	const maxPayload = 400
	if len(caps) == 0 {
		srv.sendCAP(s, subCmd, "")
		return
	}
	for len(caps) > 0 {
		end, size := 0, 0
		for end < len(caps) && size+len(caps[end])+1 <= maxPayload {
			size += len(caps[end]) + 1
			end++
		}
		if end == 0 {
			end = 1
		}
		value := strings.Join(caps[:end], " ")
		if end < len(caps) {
			nick := s.nick
			if nick == "" {
				nick = "*"
			}
			s.Send(&irc.Message{Prefix: &irc.Prefix{Nick: srv.cfg.Name}, Command: irc.CAP, Params: []string{nick, subCmd, "*", value}})
		} else {
			srv.sendCAP(s, subCmd, value)
		}
		caps = caps[end:]
	}
}

// ---------------------------------------------------------------------------
// Messaging
// ---------------------------------------------------------------------------

func (srv *Server) handlePrivmsg(s *Session, msg *irc.Message, notice bool) {
	if len(msg.Params) < 2 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, msg.Command, "Not enough parameters")
		return
	}
	target := msg.Params[0]
	text := msg.Params[1]

	cmd := irc.PRIVMSG
	if notice {
		cmd = irc.NOTICE
	}

	now := time.Now().UTC()
	tags := irc.Tags{
		"msgid": rand.Text(),
		"time":  serverTime(now),
	}
	if account, _ := s.identity(); account != "" {
		tags["account"] = account
	}
	if s.capEnabled(irc.CapMessageTags) {
		for tag, value := range msg.Tags {
			if strings.HasPrefix(tag, "+") {
				tags[tag] = value
			}
		}
	}

	outMsg := &irc.Message{
		Tags:    tags,
		Prefix:  s.Prefix(),
		Command: cmd,
		Params:  []string{target, text},
	}

	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&") {
		ch, ok := srv.channels.Get(target)
		if !ok {
			s.SendNumeric(irc.ERR_NOSUCHNICK, target, "No such nick/channel")
			return
		}
		if !ch.HasMember(s.nick) && ch.modes.Has('n') {
			s.SendNumeric(irc.ERR_CANNOTSENDTOCHAN, target, "Cannot send to channel")
			return
		}
		if ch.modes.Has('m') {
			m := ch.GetMembership(s.nick)
			if m == nil || m.Prefix == "" {
				s.SendNumeric(irc.ERR_CANNOTSENDTOCHAN, target, "Cannot send to channel (+m)")
				return
			}
		}
		ch.appendHistory(now, outMsg)
		ch.Broadcast(outMsg, s)
		if s.capEnabled(irc.CapEchoMessage) {
			s.Send(outMsg)
		}
	} else {
		dest, ok := srv.sessions.Get(target)
		if !ok {
			s.SendNumeric(irc.ERR_NOSUCHNICK, target, "No such nick/channel")
			return
		}
		s.appendHistory(dest.nick, now, outMsg)
		dest.appendHistory(s.nick, now, outMsg)
		if dest == s {
			s.sendImmediate(outMsg)
		} else {
			dest.Send(outMsg)
		}
		if s.capEnabled(irc.CapEchoMessage) {
			s.Send(outMsg)
		}
		if dest.away != "" && !notice {
			s.SendNumeric(irc.RPL_AWAY, target, dest.away)
		}
	}
}

func (srv *Server) handleTagmsg(s *Session, msg *irc.Message) {
	if len(msg.Params) < 1 || msg.Params[0] == "" {
		s.SendNumeric(irc.ERR_NORECIPIENT, "No recipient given (TAGMSG)")
		return
	}
	if !s.capEnabled(irc.CapMessageTags) {
		return
	}
	target := msg.Params[0]
	tags := irc.Tags{
		"msgid": rand.Text(),
		"time":  serverTime(time.Now()),
	}
	if account, _ := s.identity(); account != "" {
		tags["account"] = account
	}
	for tag, value := range msg.Tags {
		if strings.HasPrefix(tag, "+") {
			tags[tag] = value
		}
	}
	outMsg := &irc.Message{
		Tags:    tags,
		Prefix:  s.Prefix(),
		Command: irc.TAGMSG,
		Params:  []string{target},
	}
	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&") {
		ch, ok := srv.channels.Get(target)
		if !ok {
			s.SendNumeric(irc.ERR_NOSUCHNICK, target, "No such nick/channel")
			return
		}
		if !ch.HasMember(s.nick) && ch.modes.Has('n') {
			s.SendNumeric(irc.ERR_CANNOTSENDTOCHAN, target, "Cannot send to channel")
			return
		}
		if ch.modes.Has('m') {
			m := ch.GetMembership(s.nick)
			if m == nil || m.Prefix == "" {
				s.SendNumeric(irc.ERR_CANNOTSENDTOCHAN, target, "Cannot send to channel (+m)")
				return
			}
		}
		ch.Broadcast(outMsg, s)
		if s.capEnabled(irc.CapEchoMessage) {
			s.Send(outMsg)
		}
	} else {
		dest, ok := srv.sessions.Get(target)
		if !ok {
			s.SendNumeric(irc.ERR_NOSUCHNICK, target, "No such nick/channel")
			return
		}
		if dest == s {
			s.sendImmediate(outMsg)
		} else {
			dest.Send(outMsg)
		}
		if s.capEnabled(irc.CapEchoMessage) {
			s.Send(outMsg)
		}
	}
}

func (srv *Server) handleChatHistory(s *Session, msg *irc.Message) {
	if !s.capEnabled(irc.CapChatHistory) {
		s.SendNumeric(irc.ERR_UNKNOWNCOMMAND, irc.CHATHISTORY, "Unknown command")
		return
	}
	subcommand := strings.ToUpper(msg.Param(0))
	if subcommand == "TARGETS" {
		srv.handleChatHistoryTargets(s, msg)
		return
	}
	expected := 4
	if subcommand == "BETWEEN" {
		expected = 5
	}
	if len(msg.Params) != expected {
		srv.sendChatHistoryFail(s, "INVALID_PARAMS", msg.Param(0), "Invalid parameters")
		return
	}

	target := msg.Params[1]
	limit, err := strconv.Atoi(msg.Params[len(msg.Params)-1])
	if err != nil || limit <= 0 {
		srv.sendChatHistoryFail(s, "INVALID_PARAMS", msg.Params[0], "Invalid limit")
		return
	}
	if limit > chatHistoryLimit {
		limit = chatHistoryLimit
	}
	canonicalTarget := target
	var stored []*historyEntry
	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&") {
		ch, ok := srv.channels.Get(target)
		if !ok || !ch.HasMember(s.nick) {
			srv.sendChatHistoryFail(s, "INVALID_TARGET", msg.Params[0], "Messages could not be retrieved")
			return
		}
		canonicalTarget = ch.name
		stored = ch.historySnapshot()
	} else {
		stored = s.historySnapshot(target)
	}
	entries := make([]irc.ChatHistoryEntry, len(stored))
	for i, entry := range stored {
		entries[i] = irc.ChatHistoryEntry{Time: entry.time, Msg: entry.msg}
	}
	entries, complete, err := irc.SelectChatHistory(entries, subcommand, msg.Params[2:len(msg.Params)-1], limit)
	if err != nil {
		code := "INVALID_PARAMS"
		if strings.Contains(err.Error(), "unsupported") {
			code = "INVALID_MSGREFTYPE"
		}
		srv.sendChatHistoryFail(s, code, msg.Params[0], err.Error())
		return
	}
	srv.sendChatHistory(s, canonicalTarget, entries, complete)
}

func (srv *Server) handleChatHistoryTargets(s *Session, msg *irc.Message) {
	if len(msg.Params) != 4 {
		srv.sendChatHistoryFail(s, "INVALID_PARAMS", msg.Param(0), "Invalid parameters")
		return
	}
	first, err := time.Parse(irc.ServerTimeLayout, strings.TrimPrefix(msg.Params[1], "timestamp="))
	if err != nil || !strings.HasPrefix(msg.Params[1], "timestamp=") {
		srv.sendChatHistoryFail(s, "INVALID_PARAMS", msg.Params[0], "Invalid timestamp")
		return
	}
	second, err := time.Parse(irc.ServerTimeLayout, strings.TrimPrefix(msg.Params[2], "timestamp="))
	if err != nil || !strings.HasPrefix(msg.Params[2], "timestamp=") {
		srv.sendChatHistoryFail(s, "INVALID_PARAMS", msg.Params[0], "Invalid timestamp")
		return
	}
	limit, err := strconv.Atoi(msg.Params[3])
	if err != nil || limit <= 0 {
		srv.sendChatHistoryFail(s, "INVALID_PARAMS", msg.Params[0], "Invalid limit")
		return
	}
	if first.After(second) {
		first, second = second, first
	}
	var targets []irc.ChatHistoryEntry
	for _, ch := range srv.channels.All() {
		if !ch.HasMember(s.nick) {
			continue
		}
		entries := ch.historySnapshot()
		if len(entries) == 0 {
			continue
		}
		latest := entries[len(entries)-1]
		if latest.time.After(first) && latest.time.Before(second) {
			targets = append(targets, irc.ChatHistoryEntry{
				Time: latest.time,
				Msg:  &irc.Message{Command: irc.CHATHISTORY, Params: []string{"TARGETS", ch.name, serverTime(latest.time)}},
			})
		}
	}
	for target, entries := range s.historyTargets() {
		if len(entries) == 0 {
			continue
		}
		latest := entries[len(entries)-1]
		if latest.time.After(first) && latest.time.Before(second) {
			targets = append(targets, irc.ChatHistoryEntry{
				Time: latest.time,
				Msg:  &irc.Message{Command: irc.CHATHISTORY, Params: []string{"TARGETS", target, serverTime(latest.time)}},
			})
		}
	}
	slices.SortFunc(targets, func(a, b irc.ChatHistoryEntry) int { return b.Time.Compare(a.Time) })
	complete := len(targets) <= limit
	if len(targets) > limit {
		targets = targets[:limit]
	}
	srv.sendHistoryBatch(s, "draft/chathistory-targets", "", targets, complete)
}

func (srv *Server) sendChatHistory(s *Session, target string, entries []irc.ChatHistoryEntry, complete bool) {
	srv.sendHistoryBatch(s, "chathistory", target, entries, complete)
}

func (srv *Server) sendHistoryBatch(s *Session, batchType, target string, entries []irc.ChatHistoryEntry, complete bool) {
	batchID := ""
	if s.capEnabled(irc.CapBatch) {
		batchID = rand.Text()
		tags := irc.Tags{}
		if complete {
			tags["draft/chathistory-end"] = ""
		}
		params := []string{"+" + batchID, batchType}
		if target != "" {
			params = append(params, target)
		}
		s.Send(&irc.Message{
			Tags:    tags,
			Prefix:  &irc.Prefix{Nick: srv.cfg.Name},
			Command: irc.BATCH,
			Params:  params,
		})
	}
	for _, entry := range entries {
		out := entry.Msg.Clone()
		if batchID != "" {
			if out.Tags == nil {
				out.Tags = make(irc.Tags)
			}
			out.Tags["batch"] = batchID
		}
		s.Send(out)
	}
	if batchID != "" {
		s.Send(&irc.Message{
			Prefix:  &irc.Prefix{Nick: srv.cfg.Name},
			Command: irc.BATCH,
			Params:  []string{"-" + batchID},
		})
	}
}

func (srv *Server) sendChatHistoryFail(s *Session, code, subcommand, text string) {
	s.Send(&irc.Message{
		Prefix:  &irc.Prefix{Nick: srv.cfg.Name},
		Command: irc.FAIL,
		Params:  []string{irc.CHATHISTORY, code, subcommand, text},
	})
}

// ---------------------------------------------------------------------------
// Channel commands
// ---------------------------------------------------------------------------

func (srv *Server) handleJoin(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.JOIN, "Not enough parameters")
		return
	}

	channels := strings.Split(msg.Params[0], ",")
	keys := []string{}
	if len(msg.Params) >= 2 {
		keys = strings.Split(msg.Params[1], ",")
	}

	for i, chanName := range channels {
		if chanName == "" {
			continue
		}
		if !isValidChannelName(chanName) {
			s.SendNumeric(irc.ERR_NOSUCHCHANNEL, chanName, "Invalid channel name")
			continue
		}
		key := ""
		if i < len(keys) {
			key = keys[i]
		}

		ch := srv.channels.GetOrCreate(chanName)

		// Check key
		if k, ok := ch.modes.Arg('k'); ok && k != key {
			s.SendNumeric(irc.ERR_BADCHANNELKEY, chanName, "Cannot join channel (+k)")
			continue
		}
		// Check invite-only
		if ch.modes.Has('i') && !ch.HasMember(s.nick) {
			s.SendNumeric(irc.ERR_INVITEONLYCHAN, chanName, "Cannot join channel (+i)")
			continue
		}
		// Check limit
		if limit, ok := ch.modes.Arg('l'); ok {
			var n int
			_, _ = fmt.Sscan(limit, &n)
			if n > 0 && len(ch.Members()) >= n {
				s.SendNumeric(irc.ERR_CHANNELISFULL, chanName, "Cannot join channel (+l)")
				continue
			}
		}

		if ch.HasMember(s.nick) {
			continue // already in channel
		}

		// Give ops if first member
		prefix := ""
		if len(ch.Members()) == 0 {
			prefix = "@"
		}
		ch.AddMember(s, prefix)

		joinMsg := &irc.Message{
			Prefix:  s.Prefix(),
			Command: irc.JOIN,
			Params:  []string{chanName},
		}
		for _, member := range ch.Members() {
			out := joinMsg
			if member.Session.capEnabled(irc.CapExtendedJoin) {
				account, _ := s.identity()
				if account == "" {
					account = "*"
				}
				out = joinMsg.Clone()
				out.Params = append(out.Params, account, s.realname)
			}
			member.Session.Send(out)
		}

		// Topic
		ch.mu.RLock()
		topic := ch.topic
		topicBy := ch.topicSetBy
		topicAt := ch.topicSetAt
		ch.mu.RUnlock()

		if topic != "" {
			s.SendNumeric(irc.RPL_TOPIC, chanName, topic)
			s.SendNumeric(irc.RPL_TOPICWHOTIME, chanName, topicBy, fmt.Sprintf("%d", topicAt.Unix()))
		} else {
			s.SendNumeric(irc.RPL_NOTOPIC, chanName, "No topic is set")
		}

		if !s.capEnabled(irc.CapNoImplicitNames) {
			srv.sendNames(s, ch)
		}
	}
}

func (srv *Server) handlePart(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.PART, "Not enough parameters")
		return
	}
	reason := ""
	if len(msg.Params) >= 2 {
		reason = msg.Params[1]
	}
	for _, chanName := range strings.Split(msg.Params[0], ",") {
		ch, ok := srv.channels.Get(chanName)
		if !ok {
			s.SendNumeric(irc.ERR_NOSUCHCHANNEL, chanName, "No such channel")
			continue
		}
		if !ch.HasMember(s.nick) {
			s.SendNumeric(irc.ERR_NOTONCHANNEL, chanName, "You're not on that channel")
			continue
		}
		params := []string{chanName}
		if reason != "" {
			params = append(params, reason)
		}
		partMsg := &irc.Message{Prefix: s.Prefix(), Command: irc.PART, Params: params}
		ch.Broadcast(partMsg, nil)
		empty := ch.RemoveMember(s.nick)
		if empty {
			srv.channels.Remove(chanName)
		}
	}
}

func (srv *Server) handleTopic(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.TOPIC, "Not enough parameters")
		return
	}
	chanName := msg.Params[0]
	ch, ok := srv.channels.Get(chanName)
	if !ok {
		s.SendNumeric(irc.ERR_NOSUCHCHANNEL, chanName, "No such channel")
		return
	}
	if !ch.HasMember(s.nick) {
		s.SendNumeric(irc.ERR_NOTONCHANNEL, chanName, "You're not on that channel")
		return
	}

	if len(msg.Params) < 2 {
		// Query topic
		ch.mu.RLock()
		topic := ch.topic
		topicBy := ch.topicSetBy
		topicAt := ch.topicSetAt
		ch.mu.RUnlock()
		if topic != "" {
			s.SendNumeric(irc.RPL_TOPIC, chanName, topic)
			s.SendNumeric(irc.RPL_TOPICWHOTIME, chanName, topicBy, fmt.Sprintf("%d", topicAt.Unix()))
		} else {
			s.SendNumeric(irc.RPL_NOTOPIC, chanName, "No topic is set")
		}
		return
	}

	// Set topic (check +t)
	if ch.modes.Has('t') {
		m := ch.GetMembership(s.nick)
		if m == nil || !strings.ContainsAny(m.Prefix, "@&~") {
			s.SendNumeric(irc.ERR_CHANOPRIVSNEEDED, chanName, "You're not channel operator")
			return
		}
	}

	newTopic := msg.Params[1]
	ch.mu.Lock()
	ch.topic = newTopic
	ch.topicSetBy = s.mask()
	ch.topicSetAt = timeNow()
	ch.mu.Unlock()

	topicMsg := &irc.Message{
		Prefix:  s.Prefix(),
		Command: irc.TOPIC,
		Params:  []string{chanName, newTopic},
	}
	ch.Broadcast(topicMsg, nil)
}

func (srv *Server) handleNames(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		// Send NAMES for all channels the user is in
		for _, ch := range srv.channels.All() {
			if ch.HasMember(s.nick) {
				srv.sendNames(s, ch)
			}
		}
		return
	}
	for _, chanName := range strings.Split(msg.Params[0], ",") {
		ch, ok := srv.channels.Get(chanName)
		if !ok {
			continue
		}
		srv.sendNames(s, ch)
	}
}

func (srv *Server) sendNames(s *Session, ch *Channel) {
	multiPrefix := s.capEnabled(irc.CapMultiPrefix)
	names := ch.NamesReply(multiPrefix)
	if s.capEnabled(irc.CapUserHostInNames) {
		members := ch.Members()
		names = names[:0]
		for _, member := range members {
			prefix := member.Prefix
			if !multiPrefix && len(prefix) > 1 {
				prefix = prefix[:1]
			}
			names = append(names, prefix+member.Session.mask())
		}
	}

	// Send in chunks of ~400 chars
	const chunkMax = 400
	var line []string
	lineLen := 0
	flush := func() {
		if len(line) == 0 {
			return
		}
		s.SendNumeric(irc.RPL_NAMREPLY, "=", ch.name, strings.Join(line, " "))
		line = nil
		lineLen = 0
	}
	for _, n := range names {
		if lineLen+len(n)+1 > chunkMax {
			flush()
		}
		line = append(line, n)
		lineLen += len(n) + 1
	}
	flush()
	s.SendNumeric(irc.RPL_ENDOFNAMES, ch.name, "End of /NAMES list")
}

func (srv *Server) handleList(s *Session, msg *irc.Message) {
	s.SendNumeric(irc.RPL_LISTSTART, "Channel", "Users  Name")
	for _, ch := range srv.channels.All() {
		if ch.modes.Has('s') || ch.modes.Has('p') {
			if !ch.HasMember(s.nick) {
				continue
			}
		}
		ch.mu.RLock()
		topic := ch.topic
		ch.mu.RUnlock()
		count := fmt.Sprintf("%d", len(ch.Members()))
		s.SendNumeric(irc.RPL_LIST, ch.name, count, topic)
	}
	s.SendNumeric(irc.RPL_LISTEND, "End of /LIST")
}

func (srv *Server) handleKick(s *Session, msg *irc.Message) {
	if len(msg.Params) < 2 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.KICK, "Not enough parameters")
		return
	}
	chanName := msg.Params[0]
	target := msg.Params[1]
	reason := s.nick
	if len(msg.Params) >= 3 {
		reason = msg.Params[2]
	}

	ch, ok := srv.channels.Get(chanName)
	if !ok {
		s.SendNumeric(irc.ERR_NOSUCHCHANNEL, chanName, "No such channel")
		return
	}
	m := ch.GetMembership(s.nick)
	if m == nil {
		s.SendNumeric(irc.ERR_NOTONCHANNEL, chanName, "You're not on that channel")
		return
	}
	if !strings.ContainsAny(m.Prefix, "@&~") {
		s.SendNumeric(irc.ERR_CHANOPRIVSNEEDED, chanName, "You're not channel operator")
		return
	}
	if !ch.HasMember(target) {
		s.SendNumeric(irc.ERR_USERNOTINCHANNEL, target, chanName, "They aren't on that channel")
		return
	}
	kickMsg := &irc.Message{
		Prefix:  s.Prefix(),
		Command: irc.KICK,
		Params:  []string{chanName, target, reason},
	}
	ch.Broadcast(kickMsg, nil)
	empty := ch.RemoveMember(target)
	if empty {
		srv.channels.Remove(chanName)
	}
}

func (srv *Server) handleInvite(s *Session, msg *irc.Message) {
	if len(msg.Params) < 2 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.INVITE, "Not enough parameters")
		return
	}
	target := msg.Params[0]
	chanName := msg.Params[1]

	dest, ok := srv.sessions.Get(target)
	if !ok {
		s.SendNumeric(irc.ERR_NOSUCHNICK, target, "No such nick/channel")
		return
	}
	ch, ok := srv.channels.Get(chanName)
	if ok {
		if !ch.HasMember(s.nick) {
			s.SendNumeric(irc.ERR_NOTONCHANNEL, chanName, "You're not on that channel")
			return
		}
	}

	s.SendNumeric(irc.RPL_INVITING, target, chanName)
	dest.Send(&irc.Message{
		Prefix:  s.Prefix(),
		Command: irc.INVITE,
		Params:  []string{target, chanName},
	})

	// Broadcast invite-notify
	if ch != nil {
		for _, m := range ch.Members() {
			if m.Session != s && m.Session.capEnabled(irc.CapInviteNotify) {
				m.Session.Send(&irc.Message{
					Prefix:  s.Prefix(),
					Command: irc.INVITE,
					Params:  []string{target, chanName},
				})
			}
		}
	}
}

func (srv *Server) handleMode(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.MODE, "Not enough parameters")
		return
	}
	target := msg.Params[0]

	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&") {
		srv.handleChannelMode(s, msg, target)
	} else {
		srv.handleUserMode(s, msg, target)
	}
}

func (srv *Server) handleChannelMode(s *Session, msg *irc.Message, chanName string) {
	ch, ok := srv.channels.Get(chanName)
	if !ok {
		s.SendNumeric(irc.ERR_NOSUCHCHANNEL, chanName, "No such channel")
		return
	}

	// Query mode
	if len(msg.Params) < 2 {
		s.SendNumeric(irc.RPL_CHANNELMODEIS, chanName, ch.modes.String())
		s.SendNumeric(irc.RPL_CREATIONTIME, chanName, fmt.Sprintf("%d", ch.createdAt.Unix()))
		return
	}

	// List modes query (e.g. MODE #chan b)
	if len(msg.Params) == 2 && len(msg.Params[1]) == 1 {
		modeRune := rune(msg.Params[1][0])
		cv := mode.ChannelValidator{}
		if cv.Type(modeRune) == mode.TypeList {
			for _, entry := range ch.modes.List(modeRune) {
				s.SendNumeric(irc.RPL_BANLIST, chanName, entry)
			}
			s.SendNumeric(irc.RPL_ENDOFBANLIST, chanName, "End of ban list")
			return
		}
	}

	// Set/unset modes
	membership := ch.GetMembership(s.nick)
	if membership == nil {
		s.SendNumeric(irc.ERR_NOTONCHANNEL, chanName, "You're not on that channel")
		return
	}
	if !strings.ContainsAny(membership.Prefix, "@&~") && !s.isOper {
		s.SendNumeric(irc.ERR_CHANOPRIVSNEEDED, chanName, "You're not channel operator")
		return
	}

	changes := irc.ParseModeString(msg.Params[1], msg.Params[2:])
	var applied []irc.ModeChange
	for _, change := range changes {
		if strings.ContainsRune("qaohv", change.Mode) {
			if ch.applyMembershipMode(change) {
				applied = append(applied, change)
			}
		} else {
			applied = append(applied, ch.modes.Apply([]irc.ModeChange{change}, mode.ChannelValidator{})...)
		}
	}
	if len(applied) > 0 {
		modeStr, args := irc.FormatModeChanges(applied)
		params := append([]string{chanName, modeStr}, args...)
		ch.Broadcast(&irc.Message{
			Prefix:  s.Prefix(),
			Command: irc.MODE,
			Params:  params,
		}, nil)
	}
}

func (srv *Server) handleUserMode(s *Session, msg *irc.Message, target string) {
	if !strings.EqualFold(target, s.nick) && !s.isOper {
		s.SendNumeric(irc.ERR_USERSDONTMATCH, "Cannot change mode for other users")
		return
	}
	dest, ok := srv.sessions.Get(target)
	if !ok {
		s.SendNumeric(irc.ERR_NOSUCHNICK, target, "No such nick")
		return
	}
	if len(msg.Params) < 2 {
		s.SendNumeric(irc.RPL_UMODEIS, dest.modes.String())
		return
	}
	changes := irc.ParseModeString(msg.Params[1], msg.Params[2:])
	applied := dest.modes.Apply(changes, mode.UserValidator{})
	if len(applied) > 0 {
		modeStr, args := irc.FormatModeChanges(applied)
		params := append([]string{target, modeStr}, args...)
		dest.Send(&irc.Message{
			Prefix:  s.Prefix(),
			Command: irc.MODE,
			Params:  params,
		})
	}
}

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

func (srv *Server) handleWho(s *Session, msg *irc.Message) {
	mask := "*"
	if len(msg.Params) > 0 {
		mask = msg.Params[0]
	}

	if strings.HasPrefix(mask, "#") || strings.HasPrefix(mask, "&") {
		ch, ok := srv.channels.Get(mask)
		if ok {
			for _, m := range ch.Members() {
				u := m.Session
				flags := "H"
				if u.away != "" {
					flags = "G"
				}
				flags += m.Prefix
				_, host := u.identity()
				s.SendNumeric(irc.RPL_WHOREPLY, mask, u.user, host, srv.cfg.Name, u.nick, flags, "0 "+u.realname)
			}
		}
	} else {
		for _, u := range srv.sessions.All() {
			if matchMask(mask, u.mask()) || matchMask(mask, u.nick) {
				flags := "H"
				if u.away != "" {
					flags = "G"
				}
				_, host := u.identity()
				s.SendNumeric(irc.RPL_WHOREPLY, mask, u.user, host, srv.cfg.Name, u.nick, flags, "0 "+u.realname)
			}
		}
	}
	s.SendNumeric(irc.RPL_ENDOFWHO, mask, "End of /WHO list")
}

func (srv *Server) handleWhois(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NONICKNAMEGIVEN, "No nickname given")
		return
	}
	nick := msg.Params[0]
	target, ok := srv.sessions.Get(nick)
	if !ok {
		s.SendNumeric(irc.ERR_NOSUCHNICK, nick, "No such nick/channel")
		s.SendNumeric(irc.RPL_ENDOFWHOIS, nick, "End of /WHOIS list")
		return
	}

	account, host := target.identity()
	s.SendNumeric(irc.RPL_WHOISUSER, target.nick, target.user, host, "*", target.realname)
	if account != "" {
		s.SendNumeric(irc.RPL_WHOISACCOUNT, target.nick, account, "is logged in as")
	}
	s.SendNumeric(irc.RPL_WHOISSERVER, target.nick, srv.cfg.Name, srv.cfg.Network)

	// Channels
	var chans []string
	for _, ch := range srv.channels.All() {
		m := ch.GetMembership(target.nick)
		if m != nil {
			chans = append(chans, m.Prefix+ch.name)
		}
	}
	if len(chans) > 0 {
		s.SendNumeric(irc.RPL_WHOISCHANNELS, target.nick, strings.Join(chans, " "))
	}

	if target.isOper {
		s.SendNumeric(irc.RPL_WHOISOPERATOR, target.nick, "is an IRC operator")
	}
	if target.away != "" {
		s.SendNumeric(irc.RPL_AWAY, target.nick, target.away)
	}
	idle := int64(timeNow().Sub(target.idleAt).Seconds())
	signon := target.signOnAt.Unix()
	s.SendNumeric(irc.RPL_WHOISIDLE, target.nick, fmt.Sprintf("%d", idle), fmt.Sprintf("%d", signon), "seconds idle, signon time")
	s.SendNumeric(irc.RPL_ENDOFWHOIS, target.nick, "End of /WHOIS list")
}

func (srv *Server) handleWhowas(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NONICKNAMEGIVEN, "No nickname given")
		return
	}
	nick := msg.Params[0]
	srv.whowasMu.Lock()
	entries := append([]whowasEntry(nil), srv.whowas...)
	srv.whowasMu.Unlock()

	found := false
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if strings.EqualFold(e.nick, nick) {
			s.SendNumeric(irc.RPL_WHOWASUSER, e.nick, e.user, e.host, "*", e.realname)
			s.SendNumeric(irc.RPL_WHOISSERVER, e.nick, srv.cfg.Name, e.quitAt.Format("Mon Jan 02 15:04:05 2006"))
			found = true
		}
	}
	if !found {
		s.SendNumeric(irc.ERR_WASNOSUCHNICK, nick, "There was no such nickname")
	}
	s.SendNumeric(irc.RPL_ENDOFWHOWAS, nick, "End of WHOWAS")
}

// ---------------------------------------------------------------------------
// User state
// ---------------------------------------------------------------------------

func (srv *Server) handleAway(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 || msg.Params[0] == "" {
		s.away = ""
		s.SendNumeric(irc.RPL_UNAWAY, "You are no longer marked as being away")
	} else {
		s.away = msg.Params[0]
		s.SendNumeric(irc.RPL_NOWAWAY, "You have been marked as being away")
	}

	// away-notify broadcast
	notified := make(map[*Session]bool)
	awayMsg := &irc.Message{Prefix: s.Prefix(), Command: irc.AWAY}
	if s.away != "" {
		awayMsg.Params = []string{s.away}
	}
	for _, ch := range srv.channels.All() {
		if !ch.HasMember(s.nick) {
			continue
		}
		for _, m := range ch.Members() {
			if m.Session != s && !notified[m.Session] && m.Session.capEnabled(irc.CapAwayNotify) {
				notified[m.Session] = true
				m.Session.Send(awayMsg)
			}
		}
	}
	for _, peer := range srv.extendedMonitorPeers(s, irc.CapAwayNotify) {
		if !notified[peer] {
			notified[peer] = true
			peer.Send(awayMsg)
		}
	}
}

func (srv *Server) handleUserhost(s *Session, msg *irc.Message) {
	var results []string
	for _, nick := range msg.Params {
		u, ok := srv.sessions.Get(nick)
		if !ok {
			continue
		}
		oper := ""
		if u.isOper {
			oper = "*"
		}
		away := "+"
		if u.away != "" {
			away = "-"
		}
		_, host := u.identity()
		results = append(results, u.nick+oper+"="+away+u.user+"@"+host)
	}
	s.SendNumeric(irc.RPL_USERHOST, strings.Join(results, " "))
}

func (srv *Server) handleIson(s *Session, msg *irc.Message) {
	var online []string
	for _, nick := range msg.Params {
		if _, ok := srv.sessions.Get(nick); ok {
			online = append(online, nick)
		}
	}
	s.SendNumeric(irc.RPL_ISON, strings.Join(online, " "))
}

const monitorLimit = 100

func (srv *Server) handleMonitor(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.MONITOR, "Not enough parameters")
		return
	}
	switch strings.ToUpper(msg.Params[0]) {
	case "+":
		if len(msg.Params) < 2 {
			s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.MONITOR, "Not enough parameters")
			return
		}
		var online, offline, rejected []string
		s.monitorMu.Lock()
		for _, nick := range strings.Split(msg.Params[1], ",") {
			if !isValidNick(nick) {
				continue
			}
			key := strings.ToLower(nick)
			if _, exists := s.monitor[key]; !exists {
				if len(s.monitor) >= monitorLimit {
					rejected = append(rejected, nick)
					continue
				}
				s.monitor[key] = nick
			}
			if target, ok := srv.sessions.Get(nick); ok {
				online = append(online, target.mask())
			} else {
				offline = append(offline, nick)
			}
		}
		s.monitorMu.Unlock()
		srv.sendMonitorList(s, irc.RPL_MONONLINE, online)
		srv.sendMonitorList(s, irc.RPL_MONOFFLINE, offline)
		if len(rejected) > 0 {
			s.SendNumeric(irc.ERR_MONLISTFULL, strconv.Itoa(monitorLimit), strings.Join(rejected, ","), "Monitor list is full")
		}
	case "-":
		if len(msg.Params) < 2 {
			s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.MONITOR, "Not enough parameters")
			return
		}
		s.monitorMu.Lock()
		for _, nick := range strings.Split(msg.Params[1], ",") {
			delete(s.monitor, strings.ToLower(nick))
		}
		s.monitorMu.Unlock()
	case "C":
		s.monitorMu.Lock()
		clear(s.monitor)
		s.monitorMu.Unlock()
	case "L":
		list := s.monitorSnapshot()
		slices.SortFunc(list, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
		srv.sendMonitorList(s, irc.RPL_MONLIST, list)
		s.SendNumeric(irc.RPL_ENDOFMONLIST, "End of MONITOR list")
	case "S":
		var online, offline []string
		for _, nick := range s.monitorSnapshot() {
			if target, ok := srv.sessions.Get(nick); ok {
				online = append(online, target.mask())
			} else {
				offline = append(offline, nick)
			}
		}
		srv.sendMonitorList(s, irc.RPL_MONONLINE, online)
		srv.sendMonitorList(s, irc.RPL_MONOFFLINE, offline)
	default:
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.MONITOR, "Invalid MONITOR subcommand")
	}
}

func (srv *Server) sendMonitorList(s *Session, numeric irc.Numeric, values []string) {
	for len(values) > 0 {
		end, length := 0, 0
		for end < len(values) && length+len(values[end])+1 <= 350 {
			length += len(values[end]) + 1
			end++
		}
		if end == 0 {
			end = 1
		}
		s.SendNumeric(numeric, strings.Join(values[:end], ","))
		values = values[end:]
	}
}

func (srv *Server) notifyMonitorOnline(target *Session) {
	for _, watcher := range srv.sessions.All() {
		if watcher != target && watcher.isMonitoring(target.nick) {
			srv.sendMonitorList(watcher, irc.RPL_MONONLINE, []string{target.mask()})
		}
	}
}

func (srv *Server) notifyMonitorOffline(nick string) {
	for _, watcher := range srv.sessions.All() {
		if watcher.isMonitoring(nick) {
			srv.sendMonitorList(watcher, irc.RPL_MONOFFLINE, []string{nick})
		}
	}
}

func (srv *Server) extendedMonitorPeers(target *Session, capability string) []*Session {
	var peers []*Session
	for _, watcher := range srv.sessions.All() {
		if watcher != target && watcher.capEnabled(irc.CapExtendedMonitor) && watcher.capEnabled(capability) && watcher.isMonitoring(target.nick) {
			peers = append(peers, watcher)
		}
	}
	return peers
}

// ---------------------------------------------------------------------------
// Server commands
// ---------------------------------------------------------------------------

func (srv *Server) handlePing(s *Session, msg *irc.Message) {
	params := msg.Params
	if len(params) == 0 {
		params = []string{srv.cfg.Name}
	}
	s.Send(&irc.Message{
		Prefix:  &irc.Prefix{Nick: srv.cfg.Name},
		Command: irc.PONG,
		Params:  params,
	})
}

func (srv *Server) handleQuit(s *Session, msg *irc.Message) {
	reason := "Quit"
	if len(msg.Params) > 0 && msg.Params[0] != "" {
		reason = msg.Params[0]
	}
	s.Send(&irc.Message{
		Prefix:  &irc.Prefix{Nick: srv.cfg.Name},
		Command: irc.ERROR,
		Params:  []string{"Closing Link: " + s.Prefix().Host + " (Quit: " + reason + ")"},
	})
	s.flush()
	_ = s.conn.Close()
}

func (srv *Server) sendMOTD(s *Session) {
	if srv.cfg.MOTD == "" {
		s.SendNumeric(irc.ERR_NOMOTD, "MOTD File is missing")
		return
	}
	s.SendNumeric(irc.RPL_MOTDSTART, "- "+srv.cfg.Name+" Message of the Day -")
	for _, line := range strings.Split(srv.cfg.MOTD, "\n") {
		s.SendNumeric(irc.RPL_MOTD, "- "+line)
	}
	s.SendNumeric(irc.RPL_ENDOFMOTD, "End of /MOTD command.")
}

func (srv *Server) sendLusers(s *Session) {
	total := srv.sessions.Count()
	chans := srv.channels.Count()
	s.SendNumeric(irc.RPL_LUSERCLIENT, fmt.Sprintf("There are %d users on 1 server", total))
	s.SendNumeric(irc.RPL_LUSERCHANNELS, fmt.Sprintf("%d", chans), "channels formed")
	s.SendNumeric(irc.RPL_LUSERME, fmt.Sprintf("I have %d clients and 0 servers", total))
}

func (srv *Server) handleVersion(s *Session, _ *irc.Message) {
	s.SendNumeric(irc.RPL_VERSION, "go-irc-1.0", srv.cfg.Name, "go-irc")
}

func (srv *Server) handleTime(s *Session, _ *irc.Message) {
	s.SendNumeric(irc.RPL_TIME, srv.cfg.Name, timeNow().Format("Mon Jan 02 2006 15:04:05 -0700"))
}

func (srv *Server) handleAdmin(s *Session, _ *irc.Message) {
	s.SendNumeric(irc.RPL_ADMINME, srv.cfg.Name, "Administrative info")
	s.SendNumeric(irc.RPL_ADMINLOC1, "go-irc IRC server")
	s.SendNumeric(irc.RPL_ADMINLOC2, "Running go-irc")
	s.SendNumeric(irc.RPL_ADMINEMAIL, "admin@"+srv.cfg.Name)
}

func (srv *Server) handleInfo(s *Session, _ *irc.Message) {
	s.SendNumeric(irc.RPL_INFO, "go-irc — a Go IRC server")
	s.SendNumeric(irc.RPL_INFO, "https://github.com/natalie-o-perret/go-protocols/irc")
	s.SendNumeric(irc.RPL_ENDOFINFO, "End of /INFO list")
}

func (srv *Server) handleStats(s *Session, msg *irc.Message) {
	query := "?"
	if len(msg.Params) > 0 {
		query = msg.Params[0]
	}
	switch strings.ToLower(query) {
	case "u":
		s.SendNumeric(irc.RPL_STATSUPTIME, "Server Up 0 days, 0:00:00")
	}
	s.SendNumeric(irc.RPL_ENDOFSTATS, query, "End of /STATS report")
}

func (srv *Server) handleWallops(s *Session, msg *irc.Message) {
	if !s.isOper {
		s.SendNumeric(irc.ERR_NOPRIVILEGES, "Permission Denied- You're not an IRC operator")
		return
	}
	text := ""
	if len(msg.Params) > 0 {
		text = msg.Params[0]
	}
	wallMsg := &irc.Message{
		Prefix:  s.Prefix(),
		Command: irc.WALLOPS,
		Params:  []string{text},
	}
	for _, sess := range srv.sessions.All() {
		if sess.modes.Has('w') || sess.isOper {
			sess.Send(wallMsg)
		}
	}
}

// ---------------------------------------------------------------------------
// Oper commands
// ---------------------------------------------------------------------------

func (srv *Server) handleOper(s *Session, msg *irc.Message) {
	if len(msg.Params) < 2 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.OPER, "Not enough parameters")
		return
	}
	name := msg.Params[0]
	pass := msg.Params[1]

	hash, ok := srv.cfg.Opers[name]
	if !ok || !srv.checkPassword(s, hash, pass) {
		s.SendNumeric(irc.ERR_PASSWDMISMATCH, "Password incorrect")
		return
	}

	s.isOper = true
	s.modes.Apply([]irc.ModeChange{{Add: true, Mode: 'o'}}, mode.UserValidator{})
	s.SendNumeric(irc.RPL_YOUROPER, "You are now an IRC operator")
	s.Send(&irc.Message{
		Prefix:  &irc.Prefix{Nick: srv.cfg.Name},
		Command: irc.MODE,
		Params:  []string{s.nick, "+o"},
	})
}

func (srv *Server) handleKill(s *Session, msg *irc.Message) {
	if !s.isOper {
		s.SendNumeric(irc.ERR_NOPRIVILEGES, "Permission Denied- You're not an IRC operator")
		return
	}
	if len(msg.Params) < 2 {
		s.SendNumeric(irc.ERR_NEEDMOREPARAMS, irc.KILL, "Not enough parameters")
		return
	}
	target, ok := srv.sessions.Get(msg.Params[0])
	if !ok {
		s.SendNumeric(irc.ERR_NOSUCHNICK, msg.Params[0], "No such nick")
		return
	}
	reason := msg.Params[1]
	target.Send(&irc.Message{
		Prefix:  s.Prefix(),
		Command: irc.ERROR,
		Params:  []string{"Killed by " + s.nick + " (" + reason + ")"},
	})
	target.flush()
	_ = target.conn.Close()
}

// ---------------------------------------------------------------------------
// IRCv3 extensions
// ---------------------------------------------------------------------------

func (srv *Server) handleSetname(s *Session, msg *irc.Message) {
	if len(msg.Params) == 0 {
		return
	}
	s.realname = msg.Params[0]
	setMsg := &irc.Message{
		Prefix:  s.Prefix(),
		Command: irc.SETNAME,
		Params:  []string{s.realname},
	}
	if s.capEnabled(irc.CapSetname) {
		s.Send(setMsg)
	}
	// Broadcast to shared channels
	notified := map[string]bool{strings.ToLower(s.nick): true}
	for _, ch := range srv.channels.All() {
		if !ch.HasMember(s.nick) {
			continue
		}
		for _, m := range ch.Members() {
			key := strings.ToLower(m.Session.nick)
			if !notified[key] && m.Session.capEnabled(irc.CapSetname) {
				notified[key] = true
				m.Session.Send(setMsg)
			}
		}
	}
	for _, peer := range srv.extendedMonitorPeers(s, irc.CapSetname) {
		key := strings.ToLower(peer.nick)
		if !notified[key] {
			notified[key] = true
			peer.Send(setMsg)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func isValidNick(nick string) bool {
	if len(nick) == 0 || len(nick) > 30 {
		return false
	}
	for i, ch := range nick {
		if i == 0 {
			if !isLetter(ch) && ch != '_' && ch != '\\' && ch != '[' && ch != ']' && ch != '{' && ch != '}' && ch != '|' && ch != '^' && ch != '`' {
				return false
			}
		} else {
			if !isLetter(ch) && !isDigit(ch) && ch != '-' && ch != '_' && ch != '\\' && ch != '[' && ch != ']' && ch != '{' && ch != '}' && ch != '|' && ch != '^' && ch != '`' {
				return false
			}
		}
	}
	return true
}

func isValidChannelName(name string) bool {
	if len(name) < 2 || len(name) > 64 {
		return false
	}
	if name[0] != '#' && name[0] != '&' {
		return false
	}
	return !strings.ContainsAny(name, " \x07,")
}

func isLetter(ch rune) bool { return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') }
func isDigit(ch rune) bool  { return ch >= '0' && ch <= '9' }

func cleanUsername(u string) string {
	u = strings.TrimPrefix(u, "~")
	if len(u) > 10 {
		u = u[:10]
	}
	return u
}

func matchMask(mask, target string) bool {
	// Simple glob: * and ?
	mask = strings.ToLower(mask)
	target = strings.ToLower(target)
	return globMatch(mask, target)
}

func globMatch(pattern, s string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			if len(pattern) == 1 {
				return true
			}
			for i := range s {
				if globMatch(pattern[1:], s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			pattern = pattern[1:]
			s = s[1:]
		default:
			if len(s) == 0 || pattern[0] != s[0] {
				return false
			}
			pattern = pattern[1:]
			s = s[1:]
		}
	}
	return len(s) == 0
}

// checkBcrypt compares a bcrypt hash with a plain-text password.
func checkBcrypt(hash, password string) bool {
	return bcryptCheck([]byte(hash), []byte(password))
}

// timeNow is a substitutable clock for tests.
var timeNow = func() time.Time { return time.Now() }
