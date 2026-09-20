package ftp

import "testing"

func TestParseCommand(t *testing.T) {
	c, err := Parse("USER alice\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "USER" || c.Arg != "alice" {
		t.Fatalf("got %+v", c)
	}
	c, err = Parse("QUIT\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "QUIT" || c.Arg != "" {
		t.Fatalf("got %+v", c)
	}
}

func TestReplyString(t *testing.T) {
	r := NewReply(220, "ready")
	if got := r.String(); got != "220 ready\r\n" {
		t.Fatalf("got %q", got)
	}
	ml := NewMultiLine(211, "Features:", "UTF8", "End")
	got := ml.String()
	want := "211-Features:\r\n211-UTF8\r\n211 End\r\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestParseEPSV(t *testing.T) {
	r := NewReply(229, "Entering Extended Passive Mode (|||5000|)")
	port, err := ParseEPSVReply(r)
	if err != nil {
		t.Fatal(err)
	}
	if port != "5000" {
		t.Fatalf("got %q", port)
	}
}

func TestParsePASV(t *testing.T) {
	r := NewReply(227, "Entering Passive Mode (10,0,0,1,156,64)")
	host, port, err := ParsePASVReply(r)
	if err != nil {
		t.Fatal(err)
	}
	if host != "10.0.0.1" || port != 40000 {
		t.Fatalf("got %s:%d", host, port)
	}
}

func TestFormatPORT(t *testing.T) {
	got := FormatPORT("10.0.0.1", 40000)
	want := "10,0,0,1,156,64"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
