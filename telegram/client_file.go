//go:build linux

package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (c *Client) GetFileInfo(ctx context.Context, fileID string) (*FileInfo, error) {
	if strings.TrimSpace(fileID) == "" {
		return nil, errors.New("file_id is required")
	}

	var meta getFileResponse
	if err := c.post(ctx, "getFile", map[string]any{"file_id": fileID}, &meta); err != nil {
		return nil, err
	}
	if !meta.Ok || strings.TrimSpace(meta.Result.FilePath) == "" {
		return nil, fmt.Errorf("telegram getFile failed: %s", meta.Description)
	}
	return &FileInfo{
		Path: strings.TrimSpace(meta.Result.FilePath),
		Size: meta.Result.FileSize,
	}, nil
}

func (c *Client) DownloadFile(ctx context.Context, fileID string) ([]byte, error) {
	return c.DownloadFileChecked(ctx, fileID, 0)
}

func (c *Client) DownloadFileChecked(ctx context.Context, fileID string, maxBytes int64) ([]byte, error) {
	info, err := c.GetFileInfo(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && info.Size > 0 && info.Size > maxBytes {
		return nil, fmt.Errorf("telegram file exceeds configured size limit: %d > %d", info.Size, maxBytes)
	}

	url := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", c.token, info.Path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create file download request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download file failed: %w", c.redactError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, telegramHTTPError("downloadFile", resp)
	}
	reader := io.Reader(resp.Body)
	if maxBytes > 0 {
		reader = io.LimitReader(resp.Body, maxBytes+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read downloaded file: %w", err)
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("telegram downloaded file exceeds configured size limit: %d > %d", len(data), maxBytes)
	}
	return data, nil
}
