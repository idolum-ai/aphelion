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
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/memory"
)

// UploadsClient implements OpenAI Uploads API scaffolding for large files.
type UploadsClient struct {
	client *Client
}

// CreateUploadRequest defines the initial upload envelope.
type CreateUploadRequest struct {
	Filename string
	Bytes    int64
	Purpose  string
	MIMEType string
}

// Upload describes an OpenAI upload object.
type Upload struct {
	ID        string
	Filename  string
	Bytes     int64
	Purpose   string
	MIMEType  string
	Status    string
	ExpiresAt time.Time
}

// UploadPart describes a single appended upload part.
type UploadPart struct {
	ID        string
	UploadID  string
	Bytes     int64
	CreatedAt time.Time
}

// NewUploadsClient creates a new uploads client.
func NewUploadsClient(client *Client) (*UploadsClient, error) {
	if client == nil {
		return nil, fmt.Errorf("openai uploads: client is required")
	}
	return &UploadsClient{client: client}, nil
}

// Create starts a multipart upload session.
func (c *UploadsClient) Create(ctx context.Context, req *CreateUploadRequest) (*Upload, error) {
	if req == nil {
		return nil, fmt.Errorf("openai uploads: request is required")
	}
	if strings.TrimSpace(req.Filename) == "" {
		return nil, fmt.Errorf("openai uploads: filename is required")
	}
	if req.Bytes <= 0 {
		return nil, fmt.Errorf("openai uploads: bytes must be positive")
	}
	if strings.TrimSpace(req.Purpose) == "" {
		return nil, fmt.Errorf("openai uploads: purpose is required")
	}
	if strings.TrimSpace(req.MIMEType) == "" {
		return nil, fmt.Errorf("openai uploads: mime_type is required")
	}

	body, err := encodeJSON(map[string]any{
		"filename":  req.Filename,
		"bytes":     req.Bytes,
		"purpose":   req.Purpose,
		"mime_type": req.MIMEType,
	})
	if err != nil {
		return nil, err
	}
	httpReq, err := c.client.newRequest(ctx, http.MethodPost, "/uploads", body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	var resp uploadObject
	if err := c.client.doJSON(httpReq, &resp); err != nil {
		return nil, err
	}
	return mapUpload(resp), nil
}

// AddPart appends a binary part to an existing upload.
func (c *UploadsClient) AddPart(ctx context.Context, uploadID string, data io.Reader) (*UploadPart, error) {
	if strings.TrimSpace(uploadID) == "" {
		return nil, fmt.Errorf("openai uploads: upload id is required")
	}
	if data == nil {
		return nil, fmt.Errorf("openai uploads: data reader is required")
	}

	bodyReader, contentType, err := buildUploadPartBody(data)
	if err != nil {
		return nil, err
	}
	httpReq, err := c.client.newRequest(
		ctx,
		http.MethodPost,
		"/uploads/"+url.PathEscape(uploadID)+"/parts",
		bodyReader,
	)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", contentType)

	var partResp uploadPartObject
	if err := c.client.doJSON(httpReq, &partResp); err != nil {
		return nil, err
	}
	return mapUploadPart(partResp), nil
}

// Complete finalizes an upload by stitching ordered part IDs and returns the created file.
func (c *UploadsClient) Complete(ctx context.Context, uploadID string, partIDs []string) (*memory.StoredFile, error) {
	if strings.TrimSpace(uploadID) == "" {
		return nil, fmt.Errorf("openai uploads: upload id is required")
	}
	if len(partIDs) == 0 {
		return nil, fmt.Errorf("openai uploads: at least one part id is required")
	}

	body, err := encodeJSON(map[string]any{"part_ids": partIDs})
	if err != nil {
		return nil, err
	}
	httpReq, err := c.client.newRequest(
		ctx,
		http.MethodPost,
		"/uploads/"+url.PathEscape(uploadID)+"/complete",
		body,
	)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	var resp uploadCompleteResponse
	if err := c.client.doJSON(httpReq, &resp); err != nil {
		return nil, err
	}
	if resp.File.ID == "" {
		return nil, fmt.Errorf("openai uploads: complete response missing file")
	}
	return mapStoredFile(resp.File), nil
}

// Cancel marks an in-progress upload as canceled.
func (c *UploadsClient) Cancel(ctx context.Context, uploadID string) (*Upload, error) {
	if strings.TrimSpace(uploadID) == "" {
		return nil, fmt.Errorf("openai uploads: upload id is required")
	}

	httpReq, err := c.client.newRequest(
		ctx,
		http.MethodPost,
		"/uploads/"+url.PathEscape(uploadID)+"/cancel",
		nil,
	)
	if err != nil {
		return nil, err
	}

	var resp uploadObject
	if err := c.client.doJSON(httpReq, &resp); err != nil {
		return nil, err
	}
	return mapUpload(resp), nil
}

func buildUploadPartBody(data io.Reader) (io.Reader, string, error) {
	reader, writer := io.Pipe()
	mpw := multipart.NewWriter(writer)

	go func() {
		defer writer.Close()
		defer mpw.Close()

		part, err := mpw.CreateFormFile("data", "part.bin")
		if err != nil {
			_ = writer.CloseWithError(fmt.Errorf("openai uploads: create form file: %w", err))
			return
		}
		if _, err := io.Copy(part, data); err != nil {
			_ = writer.CloseWithError(fmt.Errorf("openai uploads: copy part data: %w", err))
			return
		}
	}()

	return reader, mpw.FormDataContentType(), nil
}

type uploadObject struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Bytes     int64  `json:"bytes"`
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
	MIMEType  string `json:"mime_type"`
	Status    string `json:"status"`
	ExpiresAt int64  `json:"expires_at"`
}

type uploadPartObject struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	UploadID  string `json:"upload_id"`
	Bytes     int64  `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
}

type uploadCompleteResponse struct {
	File fileObject `json:"file"`
}

func mapUpload(in uploadObject) *Upload {
	out := &Upload{
		ID:       in.ID,
		Filename: in.Filename,
		Bytes:    in.Bytes,
		Purpose:  in.Purpose,
		MIMEType: in.MIMEType,
		Status:   in.Status,
	}
	if in.ExpiresAt > 0 {
		out.ExpiresAt = time.Unix(in.ExpiresAt, 0).UTC()
	}
	return out
}

func mapUploadPart(in uploadPartObject) *UploadPart {
	out := &UploadPart{
		ID:       in.ID,
		UploadID: in.UploadID,
		Bytes:    in.Bytes,
	}
	if in.CreatedAt > 0 {
		out.CreatedAt = time.Unix(in.CreatedAt, 0).UTC()
	}
	return out
}

func (u *uploadPartObject) UnmarshalJSON(data []byte) error {
	type alias uploadPartObject
	var aux struct {
		alias
		Bytes json.RawMessage `json:"bytes"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*u = uploadPartObject(aux.alias)
	if len(aux.Bytes) == 0 {
		return nil
	}
	var asInt int64
	if err := json.Unmarshal(aux.Bytes, &asInt); err == nil {
		u.Bytes = asInt
		return nil
	}
	return fmt.Errorf("openai uploads: unsupported bytes field %s", string(aux.Bytes))
}
