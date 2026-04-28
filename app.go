package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	chunkStatusCompleted = 2
	maxChunkRetries      = 5
	maxHTTPConnections   = 128
)

var errUnauthorized = errors.New("session expired, please login again")

type App struct {
	ctx context.Context
	mu  sync.Mutex

	client    *http.Client
	serverURL *url.URL
	token     string
}

type LoginRequest struct {
	ServerURL     string `json:"serverUrl"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	SkipTLSVerify bool   `json:"skipTlsVerify"`
}

type LoginResult struct {
	ServerURL string `json:"serverUrl"`
	Username  string `json:"username"`
}

type UploadRequest struct {
	RemotePath  string `json:"remotePath"`
	FilePath    string `json:"filePath"`
	Concurrency int    `json:"concurrency"`
}

type UploadResult struct {
	FileName string `json:"fileName"`
	Size     int64  `json:"size"`
	Elapsed  string `json:"elapsed"`
}

type uploadMeta struct {
	ChunkSize int            `json:"chunkSize"`
	FileSize  int64          `json:"fileSize"`
	Chunks    map[string]int `json:"chunks"`
}

type uploadResponse struct {
	Success            bool `json:"success"`
	AllChunksCompleted bool `json:"allChunksCompleted"`
}

type progressEvent struct {
	CompletedChunks int     `json:"completedChunks"`
	TotalChunks     int     `json:"totalChunks"`
	CompletedBytes  int64   `json:"completedBytes"`
	TotalBytes      int64   `json:"totalBytes"`
	Percent         float64 `json:"percent"`
	Message         string  `json:"message"`
}

func NewApp() *App {
	return &App{}
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) Login(req LoginRequest) (*LoginResult, error) {
	serverURL, err := normalizeServerURL(req.ServerURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Username) == "" {
		return nil, errors.New("username is required")
	}
	if strings.TrimSpace(req.Password) == "" {
		return nil, errors.New("password is required")
	}

	client, err := newHTTP11Client(req.SkipTLSVerify, maxHTTPConnections)
	if err != nil {
		return nil, err
	}
	token, err := login(a.ctx, client, serverURL, req.Username, req.Password)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.client = client
	a.serverURL = serverURL
	a.token = token
	a.mu.Unlock()

	a.emitLog("Authenticated successfully")
	return &LoginResult{ServerURL: serverURL.String(), Username: req.Username}, nil
}

func (a *App) Logout() {
	a.clearSession()
}

func (a *App) SelectFile() (string, error) {
	if !a.hasSession() {
		return "", errors.New("please login before selecting a file")
	}
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select file to upload",
	})
}

func (a *App) Upload(req UploadRequest) (*UploadResult, error) {
	startedAt := time.Now()

	client, serverURL, token, err := a.session()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.FilePath) == "" {
		return nil, errors.New("select a file first")
	}
	if req.Concurrency <= 0 {
		req.Concurrency = 12
	}
	if req.Concurrency > 128 {
		req.Concurrency = 128
	}

	fileInfo, err := os.Stat(req.FilePath)
	if err != nil {
		return nil, fmt.Errorf("read file metadata: %w", err)
	}
	if fileInfo.IsDir() {
		return nil, errors.New("selected path is a directory")
	}

	fileName := filepath.Base(req.FilePath)
	uploadURL := buildUploadURL(serverURL, req.RemotePath, fileName)
	uploadCtx, cancelUpload := context.WithCancel(a.ctx)
	defer cancelUpload()

	meta, err := getOrCreateUploadMeta(uploadCtx, client, token, uploadURL, fileInfo.Size())
	if err != nil {
		if errors.Is(err, errUnauthorized) {
			a.expireSession()
		}
		return nil, err
	}
	if meta.ChunkSize <= 0 {
		return nil, errors.New("server returned invalid chunk size")
	}
	if meta.FileSize != fileInfo.Size() {
		return nil, fmt.Errorf("remote resumable upload size is %d bytes, selected file is %d bytes", meta.FileSize, fileInfo.Size())
	}

	jobs, completedChunks, completedBytes := chunksToUpload(meta, fileInfo.Size())
	totalChunks := len(meta.Chunks)
	a.emitProgress(progressEvent{
		CompletedChunks: completedChunks,
		TotalChunks:     totalChunks,
		CompletedBytes:  completedBytes,
		TotalBytes:      fileInfo.Size(),
		Percent:         percent(completedBytes, fileInfo.Size()),
		Message:         fmt.Sprintf("Uploading %d remaining chunks with %d workers", len(jobs), req.Concurrency),
	})

	if len(jobs) == 0 {
		return &UploadResult{FileName: fileName, Size: fileInfo.Size(), Elapsed: time.Since(startedAt).Round(time.Millisecond).String()}, nil
	}

	file, err := os.Open(req.FilePath)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	var progressMu sync.Mutex
	var failuresMu sync.Mutex
	var failures []string
	var unauthorized bool
	jobCh := make(chan int)
	var wg sync.WaitGroup

	workerCount := min(req.Concurrency, len(jobs))
	for workerID := 0; workerID < workerCount; workerID++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for chunkIndex := range jobCh {
				chunkBytes, err := uploadChunkWithRetries(uploadCtx, client, token, uploadURL, file, meta, chunkIndex)
				if err != nil {
					if errors.Is(err, errUnauthorized) {
						failuresMu.Lock()
						unauthorized = true
						failuresMu.Unlock()
						cancelUpload()
						return
					}
					failuresMu.Lock()
					failures = append(failures, fmt.Sprintf("chunk %d: %v", chunkIndex, err))
					failuresMu.Unlock()
					a.emitLog(fmt.Sprintf("Chunk %d failed after %d attempts: %v", chunkIndex, maxChunkRetries, err))
					continue
				}

				progressMu.Lock()
				completedChunks++
				completedBytes += chunkBytes
				event := progressEvent{
					CompletedChunks: completedChunks,
					TotalChunks:     totalChunks,
					CompletedBytes:  completedBytes,
					TotalBytes:      fileInfo.Size(),
					Percent:         percent(completedBytes, fileInfo.Size()),
					Message:         fmt.Sprintf("Uploaded chunk %d", chunkIndex),
				}
				progressMu.Unlock()
				a.emitProgress(event)
			}
		}()
	}

feedJobs:
	for _, chunkIndex := range jobs {
		select {
		case <-uploadCtx.Done():
			break feedJobs
		case jobCh <- chunkIndex:
		}
	}
	close(jobCh)
	wg.Wait()

	if unauthorized {
		a.expireSession()
		return nil, errUnauthorized
	}

	if len(failures) > 0 {
		return nil, fmt.Errorf("upload finished with %d failed chunks: %s", len(failures), strings.Join(failures, "; "))
	}

	return &UploadResult{FileName: fileName, Size: fileInfo.Size(), Elapsed: time.Since(startedAt).Round(time.Millisecond).String()}, nil
}

func (a *App) hasSession() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.client != nil && a.serverURL != nil && a.token != ""
}

func (a *App) session() (*http.Client, *url.URL, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil || a.serverURL == nil || a.token == "" {
		return nil, nil, "", errors.New("please login first")
	}
	return a.client, a.serverURL, a.token, nil
}

func (a *App) clearSession() {
	a.mu.Lock()
	a.client = nil
	a.serverURL = nil
	a.token = ""
	a.mu.Unlock()
}

func (a *App) expireSession() {
	a.clearSession()
	runtime.EventsEmit(a.ctx, "auth:expired")
}

func newHTTP11Client(skipTLSVerify bool, concurrency int) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}

	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		ForceAttemptHTTP2:   false,
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxConnsPerHost:     concurrency,
		MaxIdleConns:        concurrency,
		MaxIdleConnsPerHost: concurrency,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: skipTLSVerify},
	}

	return &http.Client{Jar: jar, Transport: transport}, nil
}

func login(ctx context.Context, client *http.Client, serverURL *url.URL, username, password string) (string, error) {
	loginURL := serverURL.ResolveReference(&url.URL{Path: "/login"})
	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Connection", "keep-alive")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login failed: server returned %s", resp.Status)
	}

	for _, cookie := range client.Jar.Cookies(loginURL) {
		if cookie.Name == "access_token" && cookie.Value != "" {
			return cookie.Value, nil
		}
	}
	return "", errors.New("login succeeded but no access token was returned")
}

func getOrCreateUploadMeta(ctx context.Context, client *http.Client, token string, uploadURL *url.URL, fileSize int64) (*uploadMeta, error) {
	meta, status, err := getUploadMeta(ctx, client, token, uploadURL)
	if err == nil {
		return meta, nil
	}
	if status != http.StatusNotFound {
		return nil, err
	}

	body, _ := json.Marshal(map[string]int64{"size": fileSize})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	setAuthHeader(req, token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create resumable upload failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, errUnauthorized
	}
	if resp.StatusCode != http.StatusCreated {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("create resumable upload failed: server returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
	}

	var created uploadMeta
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return nil, fmt.Errorf("decode created upload metadata: %w", err)
	}
	return &created, nil
}

func getUploadMeta(ctx context.Context, client *http.Client, token string, uploadURL *url.URL) (*uploadMeta, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uploadURL.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	setAuthHeader(req, token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("get upload metadata failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, resp.StatusCode, errUnauthorized
	}

	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, resp.StatusCode, fmt.Errorf("get upload metadata failed: server returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
	}

	var meta uploadMeta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("decode upload metadata: %w", err)
	}
	return &meta, resp.StatusCode, nil
}

func uploadChunkWithRetries(ctx context.Context, client *http.Client, token string, uploadURL *url.URL, file *os.File, meta *uploadMeta, chunkIndex int) (int64, error) {
	chunkBytes := chunkSizeForIndex(meta, chunkIndex)
	if chunkBytes < 0 {
		return 0, fmt.Errorf("invalid chunk size for chunk %d", chunkIndex)
	}

	buffer := make([]byte, chunkBytes)
	_, err := file.ReadAt(buffer, int64(chunkIndex*meta.ChunkSize))
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("read chunk data: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= maxChunkRetries; attempt++ {
		respBody, status, err := putChunk(ctx, client, token, uploadURL, chunkIndex, buffer)
		if errors.Is(err, errUnauthorized) {
			return 0, errUnauthorized
		}
		if err == nil && status == http.StatusOK && respBody.Success {
			return int64(len(buffer)), nil
		}

		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("server returned %d", status)
		}

		freshMeta, _, metaErr := getUploadMeta(ctx, client, token, uploadURL)
		if errors.Is(metaErr, errUnauthorized) {
			return 0, errUnauthorized
		}
		if metaErr == nil && freshMeta.Chunks[strconv.Itoa(chunkIndex)] == chunkStatusCompleted {
			return int64(len(buffer)), nil
		}

		time.Sleep(time.Duration(attempt*attempt) * 300 * time.Millisecond)
	}

	return 0, lastErr
}

func putChunk(ctx context.Context, client *http.Client, token string, uploadURL *url.URL, chunkIndex int, data []byte) (*uploadResponse, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL.String(), bytes.NewReader(data))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Resumable-Upload-Chunk-Index", strconv.Itoa(chunkIndex))
	req.Header.Set("Content-Type", "application/octet-stream")
	setAuthHeader(req, token)
	req.ContentLength = int64(len(data))

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("upload chunk request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, resp.StatusCode, errUnauthorized
	}

	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, resp.StatusCode, fmt.Errorf("upload chunk failed: server returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
	}

	var uploadResult uploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&uploadResult); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("decode chunk response: %w", err)
	}
	return &uploadResult, resp.StatusCode, nil
}

func setAuthHeader(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func chunksToUpload(meta *uploadMeta, fileSize int64) ([]int, int, int64) {
	jobs := make([]int, 0, len(meta.Chunks))
	completedChunks := 0
	var completedBytes int64

	for key, status := range meta.Chunks {
		chunkIndex, err := strconv.Atoi(key)
		if err != nil {
			continue
		}
		if status == chunkStatusCompleted {
			completedChunks++
			completedBytes += int64(chunkSizeForIndex(meta, chunkIndex))
			continue
		}
		jobs = append(jobs, chunkIndex)
	}
	sort.Ints(jobs)
	if completedBytes > fileSize {
		completedBytes = fileSize
	}
	return jobs, completedChunks, completedBytes
}

func chunkSizeForIndex(meta *uploadMeta, chunkIndex int) int {
	start := int64(chunkIndex * meta.ChunkSize)
	if start >= meta.FileSize {
		return -1
	}
	remaining := meta.FileSize - start
	return int(math.Min(float64(meta.ChunkSize), float64(remaining)))
}

func normalizeServerURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("server URL is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("server URL must use http or https")
	}
	if parsed.Host == "" {
		return nil, errors.New("server URL host is required")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func buildUploadURL(serverURL *url.URL, remotePath, fileName string) *url.URL {
	parts := []string{"upload"}
	for _, part := range strings.Split(remotePath, "/") {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	parts = append(parts, fileName)

	built := *serverURL
	built.Path = path.Join(append([]string{serverURL.Path}, parts...)...)
	if !strings.HasPrefix(built.Path, "/") {
		built.Path = "/" + built.Path
	}
	return &built
}

func percent(done, total int64) float64 {
	if total <= 0 {
		return 100
	}
	return float64(done) / float64(total) * 100
}

func (a *App) emitProgress(event progressEvent) {
	runtime.EventsEmit(a.ctx, "upload:progress", event)
}

func (a *App) emitLog(message string) {
	runtime.EventsEmit(a.ctx, "upload:log", message)
}
