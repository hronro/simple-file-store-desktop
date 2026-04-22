package sfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	endpoint      string
	authTransport *http.Transport
	authClient    *http.Client
}

func NewClient(endpoint string) (*Client, error) {
	endpoint = strings.TrimSpace(strings.TrimRight(endpoint, "/"))
	if endpoint == "" {
		return nil, errors.New("endpoint is required")
	}

	if _, err := url.Parse(endpoint); err != nil {
		return nil, fmt.Errorf("invalid endpoint: %w", err)
	}

	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Transport: tr,
		Timeout:   120 * time.Second,
		Jar:       jar,
	}

	return &Client{
		endpoint:      endpoint,
		authTransport: tr,
		authClient:    client,
	}, nil
}

func (c *Client) Close() {
	c.authTransport.CloseIdleConnections()
}

func (c *Client) Endpoint() string {
	return c.endpoint
}

func (c *Client) Login(ctx context.Context, username, password string) (string, error) {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)

	formBody := url.Values{}
	formBody.Set("username", username)
	formBody.Set("password", password)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.endpoint+"/login",
		strings.NewReader(formBody.Encode()),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.authClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", &RequestError{StatusCode: resp.StatusCode, Message: "login failed"}
	}

	accessToken := ""
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "access_token" {
			accessToken = cookie.Value
			break
		}
	}

	if strings.TrimSpace(accessToken) == "" {
		return "", errors.New("login succeeded but access token is missing")
	}

	return accessToken, nil
}

func (c *Client) NewAuthorizedClient() *http.Client {
	workerTransport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          1,
		MaxIdleConnsPerHost:   1,
		MaxConnsPerHost:       1,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &http.Client{
		Timeout:   120 * time.Second,
		Transport: workerTransport,
	}
}

func (c *Client) CloseWorkerClient(client *http.Client) {
	if client == nil {
		return
	}

	if tr, ok := client.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}

func (c *Client) GetResumableMeta(
	ctx context.Context,
	client *http.Client,
	token string,
	remoteFilePath string,
) (*ResumableMeta, bool, error) {
	endpoint := fmt.Sprintf("%s/upload/%s", c.endpoint, encodePath(remoteFilePath))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, err
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var meta ResumableMeta
		if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
			return nil, false, err
		}
		return &meta, true, nil

	case http.StatusNotFound:
		return nil, false, nil

	default:
		return nil, false, decodeRequestError(resp)
	}
}

func (c *Client) CreateResumableUpload(
	ctx context.Context,
	client *http.Client,
	token string,
	remoteFilePath string,
	fileSize int64,
) (*ResumableMeta, error) {
	endpoint := fmt.Sprintf("%s/upload/%s", c.endpoint, encodePath(remoteFilePath))

	payload, err := json.Marshal(CreateUploadRequest{Size: fileSize})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, decodeRequestError(resp)
	}

	var meta ResumableMeta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (c *Client) UploadChunk(
	ctx context.Context,
	client *http.Client,
	token string,
	remoteFilePath string,
	chunkIndex int,
	chunk []byte,
) (*ChunkUploadResponse, error) {
	endpoint := fmt.Sprintf("%s/upload/%s", c.endpoint, encodePath(remoteFilePath))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(chunk))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Resumable-Upload-Chunk-Index", fmt.Sprintf("%d", chunkIndex))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = int64(len(chunk))

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, decodeRequestError(resp)
	}

	var out ChunkUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}

	return &out, nil
}

func decodeRequestError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))

	var payload apiErrorResponse
	if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.Error) != "" {
		return &RequestError{StatusCode: resp.StatusCode, Message: payload.Error}
	}

	bodyString := strings.TrimSpace(string(body))
	if bodyString == "" {
		return &RequestError{StatusCode: resp.StatusCode}
	}

	return &RequestError{StatusCode: resp.StatusCode, Message: bodyString}
}

func encodePath(path string) string {
	path = strings.Trim(path, "/")
	if path == "" {
		return ""
	}

	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}

	return strings.Join(segments, "/")
}
