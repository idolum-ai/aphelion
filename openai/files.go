//go:build linux

package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/memory"
)

var _ memory.FileStore = (*FilesClient)(nil)

// FilesClient implements memory.FileStore against OpenAI files APIs.
type FilesClient struct {
	client *Client
}

// NewFilesClient creates a new file-storage client.
func NewFilesClient(client *Client) (*FilesClient, error) {
	if client == nil {
		return nil, fmt.Errorf("openai files: client is required")
	}
	return &FilesClient{client: client}, nil
}

// Put uploads a local file to OpenAI's Files API.
func (c *FilesClient) Put(ctx context.Context, localPath string, purpose string) (*memory.StoredFile, error) {
	if strings.TrimSpace(localPath) == "" {
		return nil, fmt.Errorf("openai files: local path is required")
	}
	if strings.TrimSpace(purpose) == "" {
		return nil, fmt.Errorf("openai files: purpose is required")
	}

	file, err := os.Open(localPath)
	if err != nil {
		return nil, fmt.Errorf("openai files: open file: %w", err)
	}
	defer file.Close()

	bodyReader, contentType, err := buildMultipartFileUpload(filepath.Base(localPath), purpose, file)
	if err != nil {
		return nil, err
	}

	req, err := c.client.newRequest(ctx, http.MethodPost, "/files", bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)

	var uploaded fileObject
	if err := c.client.doJSON(req, &uploaded); err != nil {
		return nil, err
	}
	return mapStoredFile(uploaded), nil
}

// Get downloads file content and returns it with current metadata.
func (c *FilesClient) Get(ctx context.Context, fileID string) (io.ReadCloser, *memory.StoredFile, error) {
	if strings.TrimSpace(fileID) == "" {
		return nil, nil, fmt.Errorf("openai files: file id is required")
	}
	meta, err := c.getFile(ctx, fileID)
	if err != nil {
		return nil, nil, err
	}

	req, err := c.client.newRequest(ctx, http.MethodGet, "/files/"+url.PathEscape(fileID)+"/content", nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.client.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("openai: request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, nil, fmt.Errorf("openai: read response: %w", readErr)
		}
		return nil, nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("openai: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}
	return resp.Body, mapStoredFile(*meta), nil
}

// Delete removes a file from OpenAI storage.
func (c *FilesClient) Delete(ctx context.Context, fileID string) error {
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("openai files: file id is required")
	}
	req, err := c.client.newRequest(ctx, http.MethodDelete, "/files/"+url.PathEscape(fileID), nil)
	if err != nil {
		return err
	}
	var deleted fileDeleteResponse
	if err := c.client.doJSON(req, &deleted); err != nil {
		return err
	}
	if !deleted.Deleted {
		return fmt.Errorf("openai files: delete did not report success for %s", fileID)
	}
	return nil
}

// List returns stored files, optionally filtered client-side by purpose.
func (c *FilesClient) List(ctx context.Context, purpose string) ([]memory.StoredFile, error) {
	req, err := c.client.newRequest(ctx, http.MethodGet, "/files", nil)
	if err != nil {
		return nil, err
	}
	var listed fileListResponse
	if err := c.client.doJSON(req, &listed); err != nil {
		return nil, err
	}
	files := make([]memory.StoredFile, 0, len(listed.Data))
	for _, file := range listed.Data {
		if purpose != "" && file.Purpose != purpose {
			continue
		}
		files = append(files, *mapStoredFile(file))
	}
	return files, nil
}

// GetMetadata fetches file metadata without downloading content.
func (c *FilesClient) GetMetadata(ctx context.Context, fileID string) (*memory.StoredFile, error) {
	file, err := c.getFile(ctx, fileID)
	if err != nil {
		return nil, err
	}
	return mapStoredFile(*file), nil
}

func (c *FilesClient) getFile(ctx context.Context, fileID string) (*fileObject, error) {
	req, err := c.client.newRequest(ctx, http.MethodGet, "/files/"+url.PathEscape(fileID), nil)
	if err != nil {
		return nil, err
	}
	var file fileObject
	if err := c.client.doJSON(req, &file); err != nil {
		return nil, err
	}
	return &file, nil
}

func buildMultipartFileUpload(filename string, purpose string, src io.Reader) (io.Reader, string, error) {
	reader, writer := io.Pipe()
	mpw := multipart.NewWriter(writer)

	go func() {
		defer writer.Close()
		defer mpw.Close()

		if err := mpw.WriteField("purpose", purpose); err != nil {
			_ = writer.CloseWithError(fmt.Errorf("openai files: write purpose field: %w", err))
			return
		}
		part, err := mpw.CreateFormFile("file", filename)
		if err != nil {
			_ = writer.CloseWithError(fmt.Errorf("openai files: create form file: %w", err))
			return
		}
		if _, err := io.Copy(part, src); err != nil {
			_ = writer.CloseWithError(fmt.Errorf("openai files: copy file data: %w", err))
			return
		}
	}()

	return reader, mpw.FormDataContentType(), nil
}

type fileObject struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Bytes     int64  `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
}

type fileListResponse struct {
	Data []fileObject `json:"data"`
}

type fileDeleteResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}

func mapStoredFile(in fileObject) *memory.StoredFile {
	file := &memory.StoredFile{
		ID:       in.ID,
		Filename: in.Filename,
		Bytes:    in.Bytes,
		Purpose:  in.Purpose,
	}
	if in.CreatedAt > 0 {
		file.CreatedAt = time.Unix(in.CreatedAt, 0).UTC()
	}
	return file
}

func (f *fileObject) UnmarshalJSON(data []byte) error {
	type alias fileObject
	var aux struct {
		alias
		Bytes json.RawMessage `json:"bytes"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*f = fileObject(aux.alias)
	if len(aux.Bytes) == 0 {
		return nil
	}
	var asInt int64
	if err := json.Unmarshal(aux.Bytes, &asInt); err == nil {
		f.Bytes = asInt
		return nil
	}
	var asString string
	if err := json.Unmarshal(aux.Bytes, &asString); err == nil {
		if asString == "" {
			return nil
		}
		n, err := strconv.ParseInt(asString, 10, 64)
		if err != nil {
			return fmt.Errorf("openai files: parse bytes %q: %w", asString, err)
		}
		f.Bytes = n
		return nil
	}
	return fmt.Errorf("openai files: unsupported bytes field %s", string(aux.Bytes))
}
