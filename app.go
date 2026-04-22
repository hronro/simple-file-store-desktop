package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hronro/simple-file-store/desktop/internal/session"
	"github.com/hronro/simple-file-store/desktop/internal/settings"
	"github.com/hronro/simple-file-store/desktop/internal/sfs"
	"github.com/hronro/simple-file-store/desktop/internal/upload"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const configAppDir = "simple-file-store-desktop"

type App struct {
	ctx             context.Context
	settingsManager *settings.Manager
	sessionManager  *session.Manager
	uploadManager   *upload.Manager
	client          *sfs.Client
}

type AuthStatus struct {
	LoggedIn  bool   `json:"loggedIn"`
	ExpiresAt string `json:"expiresAt"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type UploadStartRequest struct {
	FilePaths       []string `json:"filePaths"`
	RemoteDirectory string   `json:"remoteDirectory"`
}

type UploadStartResponse struct {
	JobID string `json:"jobId"`
}

func NewApp() (*App, error) {
	settingsManager, err := settings.NewManager(configAppDir)
	if err != nil {
		return nil, err
	}

	client, err := sfs.NewClient(settingsManager.Get().Endpoint)
	if err != nil {
		return nil, err
	}

	app := &App{
		settingsManager: settingsManager,
		sessionManager:  session.NewManager(),
		client:          client,
	}

	app.uploadManager = upload.NewManager(func(event string, payload any) {
		if app.ctx == nil {
			return
		}

		runtime.EventsEmit(app.ctx, event, payload)
	})

	return app, nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) shutdown(ctx context.Context) {
	if a.client != nil {
		a.client.Close()
	}

	if a.uploadManager != nil {
		a.uploadManager.Cancel()
	}
}

func (a *App) GetSettings() settings.Settings {
	return a.settingsManager.Get()
}

func (a *App) SaveSettings(next settings.Settings) (settings.Settings, error) {
	if currentState := a.uploadManager.State(); currentState.Running {
		return settings.Settings{}, errors.New("cannot change settings while an upload is running")
	}

	normalized, err := settings.Normalize(next)
	if err != nil {
		return settings.Settings{}, err
	}

	if current := a.settingsManager.Get(); current == normalized {
		return current, nil
	}

	client, err := sfs.NewClient(normalized.Endpoint)
	if err != nil {
		return settings.Settings{}, err
	}

	saved, err := a.settingsManager.Update(normalized)
	if err != nil {
		client.Close()
		return settings.Settings{}, err
	}

	if a.client != nil {
		a.client.Close()
	}

	a.client = client
	a.sessionManager.Clear()

	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, upload.EventAuthRequired, map[string]string{
			"reason": "settings-updated",
		})
	}

	return saved, nil
}

func (a *App) AuthStatus() AuthStatus {
	current := a.sessionManager.Snapshot()
	if strings.TrimSpace(current.AccessToken) == "" {
		return AuthStatus{LoggedIn: false}
	}

	if _, err := a.sessionManager.ValidToken(); err != nil {
		return AuthStatus{LoggedIn: false}
	}

	return AuthStatus{
		LoggedIn:  true,
		ExpiresAt: current.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (a *App) Login(request LoginRequest) (AuthStatus, error) {
	if currentState := a.uploadManager.State(); currentState.Running {
		return AuthStatus{}, errors.New("cannot login while an upload is running")
	}

	if strings.TrimSpace(request.Username) == "" {
		return AuthStatus{}, errors.New("username is required")
	}
	if strings.TrimSpace(request.Password) == "" {
		return AuthStatus{}, errors.New("password is required")
	}

	token, err := a.client.Login(a.ctx, request.Username, request.Password)
	if err != nil {
		return AuthStatus{}, err
	}

	sessionSnapshot, err := a.sessionManager.SetToken(token)
	if err != nil {
		return AuthStatus{}, err
	}

	return AuthStatus{
		LoggedIn:  true,
		ExpiresAt: sessionSnapshot.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

func (a *App) Logout() {
	a.sessionManager.Clear()
	a.uploadManager.Cancel()
}

func (a *App) PickFiles() ([]string, error) {
	selected, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select files to upload",
	})
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(selected))
	for _, item := range selected {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}

	return out, nil
}

func (a *App) StartUpload(request UploadStartRequest) (UploadStartResponse, error) {
	if len(request.FilePaths) == 0 {
		return UploadStartResponse{}, errors.New("at least one file is required")
	}

	if _, err := a.sessionManager.ValidToken(); err != nil {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, upload.EventAuthRequired, map[string]string{
				"reason": "token-expired-or-invalid",
			})
		}
		return UploadStartResponse{}, fmt.Errorf("please login again: %w", err)
	}

	normalizedFiles := make([]string, 0, len(request.FilePaths))
	seen := make(map[string]struct{}, len(request.FilePaths))
	for _, filePath := range request.FilePaths {
		absolutePath, err := filepath.Abs(strings.TrimSpace(filePath))
		if err != nil {
			return UploadStartResponse{}, err
		}

		info, err := os.Stat(absolutePath)
		if err != nil {
			return UploadStartResponse{}, err
		}

		if info.IsDir() {
			return UploadStartResponse{}, fmt.Errorf("directories are not supported: %s", absolutePath)
		}

		if _, exists := seen[absolutePath]; exists {
			continue
		}
		seen[absolutePath] = struct{}{}

		normalizedFiles = append(normalizedFiles, absolutePath)
	}

	if len(normalizedFiles) == 0 {
		return UploadStartResponse{}, errors.New("at least one file is required")
	}

	settingsSnapshot := a.settingsManager.Get()

	jobID, err := a.uploadManager.Start(a.ctx, upload.JobRequest{
		FilePaths:       normalizedFiles,
		RemoteDirectory: request.RemoteDirectory,
		ThreadCount:     settingsSnapshot.UploadThreads,
	}, a.client, func() (string, error) {
		token, tokenErr := a.sessionManager.ValidToken()
		if tokenErr != nil {
			return "", tokenErr
		}
		return token, nil
	})
	if err != nil {
		return UploadStartResponse{}, err
	}

	return UploadStartResponse{JobID: jobID}, nil
}

func (a *App) CancelUpload() {
	a.uploadManager.Cancel()
}

func (a *App) UploadState() upload.JobState {
	return a.uploadManager.State()
}
