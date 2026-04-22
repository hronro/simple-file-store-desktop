package sfs

import "fmt"

type ChunkStatus uint8

const (
	ChunkNotStarted ChunkStatus = 0
	ChunkOngoing    ChunkStatus = 1
	ChunkCompleted  ChunkStatus = 2
)

type ResumableMeta struct {
	ChunkSize int64               `json:"chunkSize"`
	FileSize  int64               `json:"fileSize"`
	Chunks    map[int]ChunkStatus `json:"chunks"`
}

type CreateUploadRequest struct {
	Size int64 `json:"size"`
}

type ChunkUploadResponse struct {
	Success            bool `json:"success"`
	AllChunksCompleted bool `json:"allChunksCompleted"`
}

type apiErrorResponse struct {
	Error string `json:"error"`
}

type RequestError struct {
	StatusCode int
	Message    string
}

func (e *RequestError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("request failed with status %d", e.StatusCode)
	}
	return fmt.Sprintf("request failed with status %d: %s", e.StatusCode, e.Message)
}
