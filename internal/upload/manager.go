package upload

import (
	"context"
	crand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hronro/simple-file-store/desktop/internal/sfs"
)

const (
	EventJobStarted   = "upload:job-started"
	EventJobFinished  = "upload:job-finished"
	EventFileStarted  = "upload:file-started"
	EventFileProgress = "upload:file-progress"
	EventFileComplete = "upload:file-complete"
	EventFileFailed   = "upload:file-failed"
	EventAuthRequired = "upload:auth-required"
)

var (
	ErrAlreadyRunning = errors.New("an upload job is already running")
	ErrAuthRequired   = errors.New("authentication required")
)

type EventEmitter func(event string, payload any)

type TokenProvider func() (string, error)

type JobRequest struct {
	FilePaths       []string
	RemoteDirectory string
	ThreadCount     int
}

type Manager struct {
	mu     sync.Mutex
	active *activeJob
	emit   EventEmitter
}

type activeJob struct {
	id     string
	cancel context.CancelFunc
}

type JobState struct {
	Running bool   `json:"running"`
	JobID   string `json:"jobId"`
}

type jobStartedPayload struct {
	JobID      string    `json:"jobId"`
	TotalFiles int       `json:"totalFiles"`
	StartedAt  time.Time `json:"startedAt"`
}

type jobFinishedPayload struct {
	JobID          string    `json:"jobId"`
	CompletedFiles int       `json:"completedFiles"`
	FailedFiles    int       `json:"failedFiles"`
	Cancelled      bool      `json:"cancelled"`
	FinishedAt     time.Time `json:"finishedAt"`
}

type fileStartedPayload struct {
	JobID           string `json:"jobId"`
	FilePath        string `json:"filePath"`
	RemotePath      string `json:"remotePath"`
	FileSize        int64  `json:"fileSize"`
	TotalChunks     int    `json:"totalChunks"`
	CompletedChunks int64  `json:"completedChunks"`
	UploadedBytes   int64  `json:"uploadedBytes"`
	ThreadCount     int    `json:"threadCount"`
}

type fileProgressPayload struct {
	JobID           string  `json:"jobId"`
	FilePath        string  `json:"filePath"`
	RemotePath      string  `json:"remotePath"`
	FileSize        int64   `json:"fileSize"`
	ThreadCount     int     `json:"threadCount"`
	UploadedBytes   int64   `json:"uploadedBytes"`
	CompletedChunks int64   `json:"completedChunks"`
	TotalChunks     int     `json:"totalChunks"`
	Progress        float64 `json:"progress"`
}

type fileCompletePayload struct {
	JobID      string    `json:"jobId"`
	FilePath   string    `json:"filePath"`
	RemotePath string    `json:"remotePath"`
	FinishedAt time.Time `json:"finishedAt"`
}

type fileFailedPayload struct {
	JobID      string    `json:"jobId"`
	FilePath   string    `json:"filePath"`
	RemotePath string    `json:"remotePath"`
	Error      string    `json:"error"`
	FailedAt   time.Time `json:"failedAt"`
}

func NewManager(emitter EventEmitter) *Manager {
	return &Manager{emit: emitter}
}

func (m *Manager) Start(
	ctx context.Context,
	request JobRequest,
	client *sfs.Client,
	tokenProvider TokenProvider,
) (string, error) {
	if len(request.FilePaths) == 0 {
		return "", errors.New("at least one file must be selected")
	}

	if request.ThreadCount < 1 {
		return "", errors.New("thread count must be at least 1")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active != nil {
		return "", ErrAlreadyRunning
	}

	jobID := buildJobID()
	jobCtx, cancel := context.WithCancel(ctx)

	m.active = &activeJob{
		id:     jobID,
		cancel: cancel,
	}

	go m.run(jobCtx, jobID, request, client, tokenProvider)

	return jobID, nil
}

func (m *Manager) Cancel() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active == nil {
		return
	}

	m.active.cancel()
}

func (m *Manager) State() JobState {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active == nil {
		return JobState{Running: false, JobID: ""}
	}

	return JobState{Running: true, JobID: m.active.id}
}

func (m *Manager) run(
	ctx context.Context,
	jobID string,
	request JobRequest,
	client *sfs.Client,
	tokenProvider TokenProvider,
) {
	defer m.finish(jobID)

	m.emit(EventJobStarted, jobStartedPayload{
		JobID:      jobID,
		TotalFiles: len(request.FilePaths),
		StartedAt:  time.Now(),
	})

	completedFiles := 0
	failedFiles := 0
	cancelled := false

	for _, filePath := range request.FilePaths {
		if ctx.Err() != nil {
			cancelled = true
			break
		}

		remotePath, err := buildRemotePath(request.RemoteDirectory, filePath)
		if err != nil {
			failedFiles++
			m.emit(EventFileFailed, fileFailedPayload{
				JobID:      jobID,
				FilePath:   filePath,
				RemotePath: "",
				Error:      err.Error(),
				FailedAt:   time.Now(),
			})
			continue
		}

		if err := m.uploadFile(ctx, jobID, request.ThreadCount, filePath, remotePath, client, tokenProvider); err != nil {
			if errors.Is(err, context.Canceled) {
				cancelled = true
				m.emit(EventFileFailed, fileFailedPayload{
					JobID:      jobID,
					FilePath:   filePath,
					RemotePath: remotePath,
					Error:      "Upload cancelled",
					FailedAt:   time.Now(),
				})
				break
			}

			failedFiles++
			m.emit(EventFileFailed, fileFailedPayload{
				JobID:      jobID,
				FilePath:   filePath,
				RemotePath: remotePath,
				Error:      err.Error(),
				FailedAt:   time.Now(),
			})

			if errors.Is(err, ErrAuthRequired) {
				m.emit(EventAuthRequired, map[string]string{
					"jobId":      jobID,
					"filePath":   filePath,
					"remotePath": remotePath,
					"reason":     "token-expired-or-invalid",
				})
				cancelled = true
				break
			}

			continue
		}

		completedFiles++
		m.emit(EventFileComplete, fileCompletePayload{
			JobID:      jobID,
			FilePath:   filePath,
			RemotePath: remotePath,
			FinishedAt: time.Now(),
		})
	}

	if ctx.Err() != nil {
		cancelled = true
	}

	m.emit(EventJobFinished, jobFinishedPayload{
		JobID:          jobID,
		CompletedFiles: completedFiles,
		FailedFiles:    failedFiles,
		Cancelled:      cancelled,
		FinishedAt:     time.Now(),
	})
}

func (m *Manager) uploadFile(
	ctx context.Context,
	jobID string,
	threadCount int,
	filePath string,
	remotePath string,
	client *sfs.Client,
	tokenProvider TokenProvider,
) error {
	stat, err := os.Stat(filePath)
	if err != nil {
		return err
	}

	if stat.IsDir() {
		return errors.New("directories are not supported")
	}

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	metaClient := client.NewAuthorizedClient()
	defer client.CloseWorkerClient(metaClient)

	token, err := tokenProvider()
	if err != nil {
		return wrapAuthError(err)
	}

	meta, exists, err := client.GetResumableMeta(ctx, metaClient, token, remotePath)
	if err != nil {
		return normalizeUploadError(err)
	}

	if !exists {
		token, err = tokenProvider()
		if err != nil {
			return wrapAuthError(err)
		}

		meta, err = client.CreateResumableUpload(ctx, metaClient, token, remotePath, stat.Size())
		if err != nil {
			return normalizeUploadError(err)
		}
	}

	if meta.ChunkSize <= 0 {
		return errors.New("server returned invalid chunk size")
	}

	if meta.FileSize != stat.Size() {
		return fmt.Errorf(
			"server already has upload metadata for a different file size (%d != %d)",
			meta.FileSize,
			stat.Size(),
		)
	}

	totalChunks := len(meta.Chunks)
	if totalChunks == 0 {
		return errors.New("server returned an empty chunk manifest")
	}

	pendingIndexes := make([]int, 0, totalChunks)
	completedCount := int64(0)
	uploadedBytes := int64(0)

	for chunkIndex := 0; chunkIndex < totalChunks; chunkIndex++ {
		status, found := meta.Chunks[chunkIndex]
		if !found || status != sfs.ChunkCompleted {
			pendingIndexes = append(pendingIndexes, chunkIndex)
			continue
		}

		completedCount++
		uploadedBytes += chunkSize(meta, chunkIndex)
	}

	workers := threadCount
	if len(pendingIndexes) == 0 {
		workers = 0
	} else if workers > len(pendingIndexes) {
		workers = len(pendingIndexes)
	}

	m.emit(EventFileStarted, fileStartedPayload{
		JobID:           jobID,
		FilePath:        filePath,
		RemotePath:      remotePath,
		FileSize:        stat.Size(),
		TotalChunks:     totalChunks,
		CompletedChunks: completedCount,
		UploadedBytes:   uploadedBytes,
		ThreadCount:     workers,
	})

	m.emit(EventFileProgress, fileProgressPayload{
		JobID:           jobID,
		FilePath:        filePath,
		RemotePath:      remotePath,
		FileSize:        stat.Size(),
		ThreadCount:     workers,
		UploadedBytes:   uploadedBytes,
		CompletedChunks: completedCount,
		TotalChunks:     totalChunks,
		Progress:        progress(uploadedBytes, stat.Size()),
	})

	if len(pendingIndexes) == 0 {
		return nil
	}

	uploadCtx, cancelUpload := context.WithCancel(ctx)
	defer cancelUpload()

	chunkQueue := make(chan int, workers*2)
	errChannel := make(chan error, 1)
	var reportOnce sync.Once

	reportError := func(err error) {
		if err == nil {
			return
		}

		reportOnce.Do(func() {
			errChannel <- err
			cancelUpload()
		})
	}

	var workersWG sync.WaitGroup
	workersWG.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer workersWG.Done()

			workerClient := client.NewAuthorizedClient()
			defer client.CloseWorkerClient(workerClient)

			for {
				select {
				case <-uploadCtx.Done():
					return

				case chunkIndex, ok := <-chunkQueue:
					if !ok {
						return
					}

					chunk, readErr := readChunk(file, meta, chunkIndex)
					if readErr != nil {
						reportError(readErr)
						return
					}

					if len(chunk) == 0 {
						reportError(fmt.Errorf("empty chunk %d", chunkIndex))
						return
					}

					if uploadErr := uploadChunkWithRetry(
						uploadCtx,
						client,
						workerClient,
						tokenProvider,
						remotePath,
						chunkIndex,
						chunk,
					); uploadErr != nil {
						reportError(uploadErr)
						return
					}

					completed := atomic.AddInt64(&completedCount, 1)
					uploaded := atomic.AddInt64(&uploadedBytes, int64(len(chunk)))

					m.emit(EventFileProgress, fileProgressPayload{
						JobID:           jobID,
						FilePath:        filePath,
						RemotePath:      remotePath,
						FileSize:        stat.Size(),
						ThreadCount:     workers,
						UploadedBytes:   uploaded,
						CompletedChunks: completed,
						TotalChunks:     totalChunks,
						Progress:        progress(uploaded, stat.Size()),
					})
				}
			}
		}()
	}

	go func() {
		defer close(chunkQueue)
		for _, chunkIndex := range pendingIndexes {
			select {
			case <-uploadCtx.Done():
				return
			case chunkQueue <- chunkIndex:
			}
		}
	}()

	workersWG.Wait()

	select {
	case workerErr := <-errChannel:
		return workerErr
	default:
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	m.emit(EventFileProgress, fileProgressPayload{
		JobID:           jobID,
		FilePath:        filePath,
		RemotePath:      remotePath,
		FileSize:        stat.Size(),
		ThreadCount:     workers,
		UploadedBytes:   atomic.LoadInt64(&uploadedBytes),
		CompletedChunks: atomic.LoadInt64(&completedCount),
		TotalChunks:     totalChunks,
		Progress:        progress(atomic.LoadInt64(&uploadedBytes), stat.Size()),
	})

	return nil
}

func uploadChunkWithRetry(
	ctx context.Context,
	apiClient *sfs.Client,
	workerClient *http.Client,
	tokenProvider TokenProvider,
	remotePath string,
	chunkIndex int,
	chunk []byte,
) error {
	var lastErr error

	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		token, tokenErr := tokenProvider()
		if tokenErr != nil {
			return wrapAuthError(tokenErr)
		}

		_, requestErr := apiClient.UploadChunk(ctx, workerClient, token, remotePath, chunkIndex, chunk)
		if requestErr == nil {
			return nil
		}

		normalized := normalizeUploadError(requestErr)
		if canTreatChunkAsComplete(normalized) {
			return nil
		}

		if !isRetryable(normalized) {
			return normalized
		}

		lastErr = normalized
		wait := time.Duration(attempt+1) * 300 * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}

	if lastErr == nil {
		lastErr = errors.New("chunk upload failed")
	}

	return lastErr
}

func (m *Manager) finish(jobID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active != nil && m.active.id == jobID {
		m.active = nil
	}
}

func chunkSize(meta *sfs.ResumableMeta, chunkIndex int) int64 {
	start := int64(chunkIndex) * meta.ChunkSize
	remaining := meta.FileSize - start
	if remaining < 0 {
		return 0
	}

	if remaining < meta.ChunkSize {
		return remaining
	}

	return meta.ChunkSize
}

func readChunk(file *os.File, meta *sfs.ResumableMeta, chunkIndex int) ([]byte, error) {
	size := chunkSize(meta, chunkIndex)
	if size <= 0 {
		return nil, fmt.Errorf("invalid chunk index %d", chunkIndex)
	}

	if size > int64(^uint(0)>>1) {
		return nil, fmt.Errorf("chunk %d is too large", chunkIndex)
	}

	data := make([]byte, int(size))
	offset := int64(chunkIndex) * meta.ChunkSize

	bytesRead, err := file.ReadAt(data, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	if int64(bytesRead) != size {
		return nil, fmt.Errorf(
			"failed to read chunk %d: expected %d bytes, got %d",
			chunkIndex,
			size,
			bytesRead,
		)
	}

	return data, nil
}

func buildRemotePath(remoteDirectory, localFilePath string) (string, error) {
	fileName := filepath.Base(localFilePath)
	if fileName == "" || fileName == "." || fileName == string(filepath.Separator) {
		return "", errors.New("invalid local file path")
	}

	normalizedDir := strings.TrimSpace(strings.ReplaceAll(remoteDirectory, "\\", "/"))
	normalizedDir = strings.Trim(normalizedDir, "/")
	if normalizedDir == "" {
		return fileName, nil
	}

	cleanedDir := path.Clean("/" + normalizedDir)
	cleanedDir = strings.TrimPrefix(cleanedDir, "/")
	if strings.HasPrefix(cleanedDir, "..") {
		return "", errors.New("remote directory cannot escape root")
	}

	return cleanedDir + "/" + fileName, nil
}

func progress(uploadedBytes, fileSize int64) float64 {
	if fileSize <= 0 {
		return 0
	}

	value := float64(uploadedBytes) / float64(fileSize)
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}

	return value
}

func buildJobID() string {
	entropy := [3]byte{}
	if _, err := crand.Read(entropy[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}

	return fmt.Sprintf(
		"%d-%02x%02x%02x",
		time.Now().UnixMilli(),
		entropy[0],
		entropy[1],
		entropy[2],
	)
}

func wrapAuthError(err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%w: %v", ErrAuthRequired, err)
}

func normalizeUploadError(err error) error {
	if err == nil {
		return nil
	}

	var requestErr *sfs.RequestError
	if errors.As(err, &requestErr) && requestErr.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w: %v", ErrAuthRequired, err)
	}

	return err
}

func canTreatChunkAsComplete(err error) bool {
	var requestErr *sfs.RequestError
	if !errors.As(err, &requestErr) {
		return false
	}

	if requestErr.StatusCode != http.StatusBadRequest {
		return false
	}

	message := strings.ToLower(requestErr.Message)
	return strings.Contains(message, "chunk") && strings.Contains(message, "completed")
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.Canceled) {
		return false
	}

	if errors.Is(err, ErrAuthRequired) {
		return false
	}

	var requestErr *sfs.RequestError
	if errors.As(err, &requestErr) {
		switch requestErr.StatusCode {
		case http.StatusRequestTimeout, http.StatusTooManyRequests:
			return true
		}

		if requestErr.StatusCode >= 400 && requestErr.StatusCode < 500 {
			return false
		}
	}

	return true
}
