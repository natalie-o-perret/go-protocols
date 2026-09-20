package ftp

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseEPSVReply extracts the port from a 229 reply to EPSV. The
// expected form is "229 Entering Extended Passive Mode (|||port|)".
// Some servers omit the trailing pipe; both are accepted.
func ParseEPSVReply(r Reply) (string, error) {
	if r.Code != CodeEnterEPSV {
		return "", fmt.Errorf("ftp: expected 229, got %d", r.Code)
	}
	text := r.Text
	if len(r.Lines) > 0 {
		text = r.Lines[len(r.Lines)-1]
	}
	open := strings.Index(text, "(")
	close := strings.LastIndex(text, ")")
	if open < 0 || close < 0 || close <= open {
		return "", fmt.Errorf("ftp: malformed EPSV reply %q", text)
	}
	parts := strings.Split(text[open+1:close], "|")
	if len(parts) < 4 || len(parts) > 5 {
		return "", fmt.Errorf("ftp: malformed EPSV reply %q", text)
	}
	if parts[0] != "" || parts[1] != "" {
		return "", fmt.Errorf("ftp: malformed EPSV reply %q", text)
	}
	return parts[len(parts)-2], nil
}

// ParsePASVReply extracts the host and port from a 227 reply to PASV.
// The expected form is "227 Entering Passive Mode (h1,h2,h3,h4,p1,p2)".
func ParsePASVReply(r Reply) (host string, port int, err error) {
	if r.Code != CodeEnterPASV {
		return "", 0, fmt.Errorf("ftp: expected 227, got %d", r.Code)
	}
	text := r.Text
	if len(r.Lines) > 0 {
		text = r.Lines[len(r.Lines)-1]
	}
	open := strings.Index(text, "(")
	close := strings.LastIndex(text, ")")
	if open < 0 || close < 0 || close <= open {
		return "", 0, fmt.Errorf("ftp: malformed PASV reply %q", text)
	}
	parts := strings.Split(text[open+1:close], ",")
	if len(parts) != 6 {
		return "", 0, fmt.Errorf("ftp: malformed PASV reply %q", text)
	}
	host = strings.Join(parts[:4], ".")
	p1, err1 := strconv.Atoi(strings.TrimSpace(parts[4]))
	p2, err2 := strconv.Atoi(strings.TrimSpace(parts[5]))
	if err1 != nil || err2 != nil {
		return "", 0, fmt.Errorf("ftp: malformed PASV reply %q", text)
	}
	return host, p1*256 + p2, nil
}

// FormatPORT builds the argument for the PORT command from a host
// and port. The result is "h1,h2,h3,h4,p1,p2".
func FormatPORT(host string, port int) string {
	parts := strings.Split(host, ".")
	hi, _ := strconv.Atoi(parts[0])
	ho, _ := strconv.Atoi(parts[1])
	lo, _ := strconv.Atoi(parts[2])
	lo2, _ := strconv.Atoi(parts[3])
	return fmt.Sprintf("%d,%d,%d,%d,%d,%d", hi, ho, lo, lo2, port/256, port%256)
}

// FormatEPRT builds the argument for the EPRT command. The network
// protocol identifier is 1 for IPv4 and 2 for IPv6.
func FormatEPRT(proto, host string, port int) string {
	return fmt.Sprintf("%s|%s|%d|", proto, host, port)
}
