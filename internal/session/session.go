package session

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrMissingToken = errors.New("not logged in")
	ErrTokenExpired = errors.New("token expired")
)

const expirySkew = 30 * time.Second

type tokenPayload struct {
	Exp int64 `json:"exp"`
}

type Session struct {
	AccessToken string
	ExpiresAt   time.Time
}

type Manager struct {
	mu      sync.RWMutex
	session Session
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) SetToken(token string) (Session, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Session{}, ErrMissingToken
	}

	exp, err := extractExpiry(token)
	if err != nil {
		return Session{}, err
	}

	now := time.Now()
	if exp.Before(now.Add(expirySkew)) {
		return Session{}, ErrTokenExpired
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.session = Session{
		AccessToken: token,
		ExpiresAt:   exp,
	}

	return m.session, nil
}

func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.session = Session{}
}

func (m *Manager) IsLoggedIn() bool {
	_, err := m.ValidToken()
	return err == nil
}

func (m *Manager) ValidToken() (string, error) {
	m.mu.RLock()
	current := m.session
	m.mu.RUnlock()

	if strings.TrimSpace(current.AccessToken) == "" {
		return "", ErrMissingToken
	}

	if current.ExpiresAt.Before(time.Now().Add(expirySkew)) {
		m.Clear()
		return "", ErrTokenExpired
	}

	return current.AccessToken, nil
}

func (m *Manager) Snapshot() Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.session
}

func extractExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("invalid token format")
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid token payload")
	}

	var payload tokenPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return time.Time{}, fmt.Errorf("invalid token claims")
	}

	if payload.Exp <= 0 {
		return time.Time{}, fmt.Errorf("token missing exp")
	}

	return time.Unix(payload.Exp, 0), nil
}
