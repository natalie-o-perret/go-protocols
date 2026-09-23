package client

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/natalie-o-perret/go-protocols/irc/irc"
)

type framingMechanism struct {
	response   []byte
	challenges [][]byte
}

func (m *framingMechanism) Name() string { return "TEST" }

func (m *framingMechanism) Next(challenge []byte) ([]byte, bool, error) {
	m.challenges = append(m.challenges, append([]byte(nil), challenge...))
	return m.response, true, nil
}

func clientWithWriter() (*Client, *bytes.Buffer) {
	c := New(Config{})
	buf := &bytes.Buffer{}
	c.writer = bufio.NewWriter(buf)
	return c, buf
}

func TestCAPWireFormatAndDisable(t *testing.T) {
	c, output := clientWithWriter()
	c.handleCAP(c, irc.MustParse(":server CAP nick LS :message-tags"))
	if got, want := output.String(), "CAP REQ message-tags\r\n"; got != want {
		t.Fatalf("CAP REQ: got %q want %q", got, want)
	}

	output.Reset()
	c.capState.enabled[irc.CapMessageTags] = true
	c.handleCAP(c, irc.MustParse(":server CAP nick ACK :-message-tags"))
	if c.CAPEnabled(irc.CapMessageTags) {
		t.Error("negative ACK left message-tags enabled")
	}
	if got, want := output.String(), "CAP END\r\n"; got != want {
		t.Fatalf("CAP END: got %q want %q", got, want)
	}
}

func TestCAPNEWRequestsDesiredCapability(t *testing.T) {
	c, output := clientWithWriter()
	c.capState.phase = capPhaseDone
	c.capState.advertised[irc.CapMessageTags] = "" // previously rejected
	c.handleCAP(c, irc.MustParse(":server CAP nick NEW :standard-replies"))

	if got, want := output.String(), "CAP REQ standard-replies\r\n"; got != want {
		t.Fatalf("CAP REQ: got %q want %q", got, want)
	}
	output.Reset()
	c.handleCAP(c, irc.MustParse(":server CAP nick ACK :standard-replies"))
	if !c.CAPEnabled(irc.CapStandardReplies) || output.Len() != 0 {
		t.Fatalf("dynamic ACK: enabled=%v output=%q", c.CAPEnabled(irc.CapStandardReplies), output.String())
	}
}

func TestCAPNEWDynamicallyStartsSASL(t *testing.T) {
	mechanism := &framingMechanism{}
	c := New(Config{SASL: mechanism})
	output := &bytes.Buffer{}
	c.writer = bufio.NewWriter(output)
	c.capState.phase = capPhaseDone
	c.handleCAP(c, irc.MustParse(":server CAP nick NEW :sasl=TEST"))
	c.handleCAP(c, irc.MustParse(":server CAP nick ACK :sasl"))

	if got := output.String(); got != "CAP REQ sasl\r\nAUTHENTICATE TEST\r\n" {
		t.Fatalf("dynamic SASL: %q", got)
	}
}

func TestCAPACKDoesNotRestartActiveSASL(t *testing.T) {
	mechanism := &framingMechanism{}
	c := New(Config{SASL: mechanism})
	output := &bytes.Buffer{}
	c.writer = bufio.NewWriter(output)
	c.capState.phase = capPhaseSASL
	c.capState.enabled[irc.CapSASL] = true
	c.handleCAP(c, irc.MustParse(":server CAP nick ACK :standard-replies"))
	if output.Len() != 0 || c.capState.phase != capPhaseSASL {
		t.Fatalf("active SASL was disturbed: phase=%v output=%q", c.capState.phase, output.String())
	}
}

func TestDisableDefaultCaps(t *testing.T) {
	c := New(Config{DisableDefaultCaps: true, RequestedCaps: []string{irc.CapServerTime}})
	c.capState.advertised[irc.CapServerTime] = ""
	c.capState.advertised[irc.CapAccountTag] = ""
	if got := c.capWantList(); len(got) != 1 || got[0] != irc.CapServerTime {
		t.Fatalf("requested caps: %v", got)
	}
}

func TestSASLIncomingChunks(t *testing.T) {
	c, _ := clientWithWriter()
	mechanism := &framingMechanism{}
	c.saslMech = mechanism

	c.handleAuthenticate(c, &irc.Message{Params: []string{strings.Repeat("A", 400)}})
	if len(mechanism.challenges) != 0 {
		t.Fatal("400-byte chunk completed the challenge")
	}
	c.handleAuthenticate(c, &irc.Message{Params: []string{"YQ=="}})
	if len(mechanism.challenges) != 1 || len(mechanism.challenges[0]) != 301 {
		t.Fatalf("challenge lengths: %v", len(mechanism.challenges))
	}
}

func TestSASLOutgoingExactChunk(t *testing.T) {
	c, output := clientWithWriter()
	c.saslMech = &framingMechanism{response: make([]byte, 300)}
	c.handleAuthenticate(c, &irc.Message{Params: []string{"+"}})

	lines := strings.Split(strings.TrimSuffix(output.String(), "\r\n"), "\r\n")
	if len(lines) != 2 || len(strings.TrimPrefix(lines[0], "AUTHENTICATE ")) != 400 || lines[1] != "AUTHENTICATE +" {
		t.Fatalf("unexpected SASL chunks: %q", lines)
	}
}
