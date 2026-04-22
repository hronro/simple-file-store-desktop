package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	defaultEndpoint      = "http://127.0.0.1:8080"
	defaultUploadThreads = 6
	minUploadThreads     = 1
	maxUploadThreads     = 64
)

var ErrInvalidSettings = errors.New("invalid settings")

type Settings struct {
	Endpoint      string `json:"endpoint"`
	UploadThreads int    `json:"uploadThreads"`
}

type Manager struct {
	mu       sync.RWMutex
	filePath string
	settings Settings
}

func Default() Settings {
	return Settings{
		Endpoint:      defaultEndpoint,
		UploadThreads: defaultUploadThreads,
	}
}

func NewManager(appDirName string) (*Manager, error) {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}

	filePath := filepath.Join(configRoot, appDirName, "settings.json")

	manager := &Manager{
		filePath: filePath,
		settings: Default(),
	}

	if err := manager.load(); err != nil {
		return nil, err
	}

	return manager, nil
}

func (m *Manager) Get() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings
}

func (m *Manager) Update(next Settings) (Settings, error) {
	normalized, err := Normalize(next)
	if err != nil {
		return Settings{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.settings = normalized

	if err := m.persistLocked(); err != nil {
		return Settings{}, err
	}

	return m.settings, nil
}

func Normalize(input Settings) (Settings, error) {
	endpoint := strings.TrimSpace(input.Endpoint)
	if endpoint == "" {
		return Settings{}, fmt.Errorf("%w: endpoint is required", ErrInvalidSettings)
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return Settings{}, fmt.Errorf("%w: endpoint is not a valid URL", ErrInvalidSettings)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return Settings{}, fmt.Errorf("%w: endpoint must use http or https", ErrInvalidSettings)
	}

	if parsed.Host == "" {
		return Settings{}, fmt.Errorf("%w: endpoint host is required", ErrInvalidSettings)
	}

	endpointPath := strings.TrimSuffix(parsed.Path, "/")
	if endpointPath == "/" {
		endpointPath = ""
	}
	parsed.Path = endpointPath
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""

	threads := input.UploadThreads
	if threads < minUploadThreads || threads > maxUploadThreads {
		return Settings{}, fmt.Errorf(
			"%w: uploadThreads must be between %d and %d",
			ErrInvalidSettings,
			minUploadThreads,
			maxUploadThreads,
		)
	}

	return Settings{
		Endpoint:      strings.TrimSuffix(parsed.String(), "/"),
		UploadThreads: threads,
	}, nil
}

func (m *Manager) load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	content, err := os.ReadFile(m.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return m.persistLocked()
		}
		return err
	}

	var loaded Settings
	if err := json.Unmarshal(content, &loaded); err != nil {
		return err
	}

	normalized, err := Normalize(loaded)
	if err != nil {
		return err
	}

	m.settings = normalized
	return nil
}

func (m *Manager) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.filePath), 0o755); err != nil {
		return err
	}

	payload, err := json.MarshalIndent(m.settings, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(m.filePath, payload, 0o644)
}
