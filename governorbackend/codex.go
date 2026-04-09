//go:build linux

package governorbackend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

type CodexOptions struct {
	BaseURL     string
	AccessToken string
	HTTPClient  *http.Client
	UserAgent   string
}

type Codex struct {
	baseURL     string
	accessToken string
	client      *http.Client
	userAgent   string
}

var _ agent.Provider = (*Codex)(nil)

func NewCodex(opts CodexOptions) (*Codex, error) {
	if strings.TrimSpace(opts.BaseURL) == "" {
		return nil, fmt.Errorf("codex: base url is required")
	}
	if strings.TrimSpace(opts.AccessToken) == "" {
		return nil, fmt.Errorf("codex: access token is required")
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	return &Codex{
		baseURL:     opts.BaseURL,
		accessToken: opts.AccessToken,
		client:      client,
		userAgent:   opts.UserAgent,
	}, nil
}

func (c *Codex) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	reqBody := codexRequest{
		Messages: toCodexMessages(messages),
		Tools:    toCodexTools(tools),
		Stream:   false,
	}

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("codex: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, &body)
	if err != nil {
		return nil, fmt.Errorf("codex: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex: request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("codex: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("codex: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))),
		}
	}

	res, err := parseCodexResponse(raw)
	if err != nil {
		return nil, fmt.Errorf("codex: decode response: %w", err)
	}
	return res, nil
}

type codexRequest struct {
	Messages []codexMessage `json:"messages"`
	Tools    []codexTool    `json:"tools,omitempty"`
	Stream   bool           `json:"stream"`
}

type codexMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []agent.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type codexTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

func toCodexMessages(messages []agent.Message) []codexMessage {
	out := make([]codexMessage, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "" {
			continue
		}
		out = append(out, codexMessage{
			Role:       role,
			Content:    msg.Content,
			ToolCalls:  append([]agent.ToolCall(nil), msg.ToolCalls...),
			ToolCallID: msg.ToolCallID,
		})
	}
	return out
}

func toCodexTools(tools []agent.ToolDef) []codexTool {
	out := make([]codexTool, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" {
			continue
		}
		out = append(out, codexTool{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
		})
	}
	return out
}

type codexResponse struct {
	Content    string           `json:"content"`
	OutputText string           `json:"output_text"`
	ToolCalls  []agent.ToolCall `json:"tool_calls"`
	Usage      struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
		TotalTokens  int64 `json:"total_tokens"`
	} `json:"usage"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func parseCodexResponse(raw []byte) (*agent.Response, error) {
	var parsed codexResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}

	content := strings.TrimSpace(parsed.OutputText)
	if content == "" {
		content = strings.TrimSpace(parsed.Content)
	}
	if content == "" {
		for _, item := range parsed.Output {
			for _, block := range item.Content {
				if strings.EqualFold(block.Type, "output_text") || strings.EqualFold(block.Type, "text") {
					if strings.TrimSpace(block.Text) != "" {
						content = block.Text
						break
					}
				}
			}
			if content != "" {
				break
			}
		}
	}

	usage := core.TokenUsage{
		InputTokens:  parsed.Usage.InputTokens,
		OutputTokens: parsed.Usage.OutputTokens,
		TotalTokens:  parsed.Usage.TotalTokens,
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}

	return &agent.Response{
		Content:   content,
		ToolCalls: parsed.ToolCalls,
		Usage:     usage,
	}, nil
}

type apiError struct {
	statusCode int
	message    string
}

func (e apiError) Error() string {
	return e.message
}

func (e apiError) StatusCode() int {
	return e.statusCode
}
