// Package auth provides pluggable authenticators for the FTP server.
//
// The simplest authenticator is AllowAnonymous, which accepts any
// non-empty user with a password matching a configured value, or
// any password if the user is "anonymous".
package auth

import (
	"errors"
	"strings"
	"sync"
)

// User is the resolved user record. The server does not interpret
// the fields; they are passed to the filesystem backend.
type User struct {
	Name string
	Home string
}

// Authenticator maps a (user, password) pair to a User record.
type Authenticator interface {
	Authenticate(user, pass string) (User, error)
}

// Static is a static (user, password) -> User map authenticator.
type Static struct {
	mu   sync.RWMutex
	rows map[string]staticRow
}

type staticRow struct {
	pass string
	user User
}

// NewStatic returns an empty Static authenticator. Use Add to
// register users.
func NewStatic() *Static { return &Static{rows: map[string]staticRow{}} }

// Add registers a (user, pass) pair.
func (s *Static) Add(user, pass string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[strings.ToLower(user)] = staticRow{pass: pass, user: User{Name: user}}
}

// Authenticate implements Authenticator.
func (s *Static) Authenticate(user, pass string) (User, error) {
	s.mu.RLock()
	row, ok := s.rows[strings.ToLower(user)]
	s.mu.RUnlock()
	if !ok || row.pass != pass {
		return User{}, errors.New("login incorrect")
	}
	return row.user, nil
}

// AnonymousConfig configures an AllowAnonymous authenticator.
type AnonymousConfig struct {
	// User is the user name that is accepted. Default "anonymous".
	User string
	// AllowedPasswords is the set of acceptable passwords for the
	// anonymous user. If empty, any non-empty password is accepted.
	AllowedPasswords map[string]struct{}
}

// AnonymousAuthenticator accepts anonymous logins. The user is
// always "anonymous"; a non-empty password is required unless the
// password is in AllowedPasswords.
type AnonymousAuthenticator struct {
	cfg AnonymousConfig
}

// NewAnonymous returns an AnonymousAuthenticator.
func NewAnonymous(cfg AnonymousConfig) *AnonymousAuthenticator {
	if cfg.User == "" {
		cfg.User = "anonymous"
	}
	return &AnonymousAuthenticator{cfg: cfg}
}

// AllowAnonymous returns an authenticator that accepts any login.
// Useful for development and tests; do not use in production.
func AllowAnonymous() *AnonymousAuthenticator {
	return &AnonymousAuthenticator{cfg: AnonymousConfig{User: "anonymous"}}
}

// Authenticate implements Authenticator.
func (a *AnonymousAuthenticator) Authenticate(user, pass string) (User, error) {
	if !strings.EqualFold(user, a.cfg.User) {
		return User{}, errors.New("login incorrect")
	}
	if pass == "" {
		return User{}, errors.New("password required")
	}
	if len(a.cfg.AllowedPasswords) > 0 {
		if _, ok := a.cfg.AllowedPasswords[pass]; !ok {
			return User{}, errors.New("login incorrect")
		}
	}
	return User{Name: user}, nil
}
