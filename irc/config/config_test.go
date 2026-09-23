package config

import (
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestServerAccountAndSTSValidation(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Server: &ServerConfig{
		TLSListen:   ":6697",
		TLSCertFile: "cert.pem",
		TLSKeyFile:  "key.pem",
		Accounts:    []AccountConfig{{Name: "alice", Password: string(hash), Host: "alice.example"}},
		STS:         &STSConfig{Port: 6697, Duration: time.Hour, Hostnames: []string{"irc.example"}},
	}}
	if err := cfg.validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	cfg.Server.Accounts = append(cfg.Server.Accounts, cfg.Server.Accounts[0])
	if err := cfg.validate(); err == nil {
		t.Fatal("duplicate account accepted")
	}
}
