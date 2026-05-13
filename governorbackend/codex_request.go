//go:build linux

package governorbackend

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

func buildCodexRequest(plan codexRequestPlan, tools []agent.ToolDef, opts agent.CompleteOptions, stream bool, model string, store bool) map[string]any {
	model = strings.TrimSpace(model)
	if model == "" {
		model = defaultCodexModel
	}
	reqBody := map[string]any{
		"model":        model,
		"instructions": plan.instructions,
		"input":        plan.input,
		"store":        store,
		"stream":       stream,
	}
	if store && plan.previousResponseID != "" {
		reqBody["previous_response_id"] = plan.previousResponseID
	}
	if defs := toCodexTools(tools); len(defs) > 0 {
		reqBody["tools"] = defs
		reqBody["tool_choice"] = "auto"
	}
	if reasoning := mapCodexReasoning(opts.Reasoning); len(reasoning) > 0 {
		reqBody["reasoning"] = reasoning
	}
	return reqBody
}

func collectCodexInstructions(messages []agent.Message) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "system") {
			continue
		}
		text := strings.TrimSpace(msg.Content)
		if text == "" && len(msg.SystemBlocks) > 0 {
			text = renderSystemBlocks(msg.SystemBlocks)
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return defaultCodexPrompt
	}
	return strings.Join(parts, "\n\n")
}

func renderSystemBlocks(blocks []agent.SystemBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		text := strings.TrimSpace(block.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func codexMessageInputItem(role string, msg agent.Message) (map[string]any, bool) {
	content := make([]map[string]any, 0, len(msg.Media)+1)
	for _, media := range msg.Media {
		if part, ok := mediaToCodexInputItem(media); ok {
			content = append(content, part)
		}
	}
	textType := "input_text"
	if role == "assistant" {
		textType = "output_text"
	}
	if text := strings.TrimSpace(msg.Content); text != "" || len(content) == 0 {
		content = append(content, map[string]any{
			"type": textType,
			"text": msg.Content,
		})
	}
	if len(content) == 0 {
		return nil, false
	}
	return map[string]any{
		"type":    "message",
		"role":    role,
		"content": content,
	}, true
}

func mediaToCodexInputItem(media core.Media) (map[string]any, bool) {
	mimeType := strings.TrimSpace(media.MimeType)
	if mimeType == "" && len(media.Data) > 0 {
		mimeType = http.DetectContentType(media.Data)
	}
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") || len(media.Data) == 0 {
		return nil, false
	}
	return map[string]any{
		"type":      "input_image",
		"image_url": fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(media.Data)),
	}, true
}

func normalizeArguments(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "{}"
	}
	if json.Valid(trimmed) {
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err == nil {
			nested := bytes.TrimSpace([]byte(encoded))
			if len(nested) > 0 && json.Valid(nested) {
				return string(nested)
			}
		}
		return string(trimmed)
	}
	quoted, err := json.Marshal(string(trimmed))
	if err != nil {
		return "{}"
	}
	return string(quoted)
}

func toCodexTools(tools []agent.ToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		if builtin, ok := codexBuiltInToolSpec(name, tool.Parameters); ok {
			out = append(out, builtin)
			continue
		}
		entry := map[string]any{
			"type": "function",
			"name": name,
		}
		if desc := strings.TrimSpace(tool.Description); desc != "" {
			entry["description"] = desc
		}
		if len(bytes.TrimSpace(tool.Parameters)) > 0 {
			entry["parameters"] = json.RawMessage(tool.Parameters)
		}
		out = append(out, entry)
	}
	return out
}

func codexBuiltInToolSpec(name string, params json.RawMessage) (map[string]any, bool) {
	if strings.TrimSpace(name) != "image_generation" {
		return nil, false
	}
	var cfg struct {
		Type         string `json:"type"`
		OutputFormat string `json:"output_format"`
	}
	if len(bytes.TrimSpace(params)) > 0 {
		_ = json.Unmarshal(params, &cfg)
	}
	if strings.TrimSpace(cfg.Type) != "builtin" {
		return nil, false
	}
	outputFormat := strings.TrimSpace(cfg.OutputFormat)
	if outputFormat == "" {
		outputFormat = "png"
	}
	return map[string]any{
		"type":          "image_generation",
		"output_format": outputFormat,
	}, true
}

func mapCodexReasoning(cfg agent.ReasoningConfig) map[string]any {
	out := map[string]any{}
	switch cfg.Effort {
	case agent.ReasoningEffortLow:
		out["effort"] = "low"
	case agent.ReasoningEffortMedium:
		out["effort"] = "medium"
	case agent.ReasoningEffortHigh:
		out["effort"] = "high"
	case agent.ReasoningEffortXHigh:
		out["effort"] = "xhigh"
	}
	switch cfg.Summary {
	case agent.ReasoningSummaryAuto:
		out["summary"] = "auto"
	case agent.ReasoningSummaryCompact:
		out["summary"] = "concise"
	}
	return out
}

type codexTurnMode string

type codexRequestPlan struct {
	mode               codexTurnMode
	instructions       string
	input              []map[string]any
	previousResponseID string
}

func planCodexRequest(messages []agent.Message) codexRequestPlan {
	if previousResponseID, input, ok := planCodexIncrementalToolResults(messages); ok {
		return codexRequestPlan{
			mode:               codexTurnModeIncrementalToolResults,
			instructions:       collectCodexInstructions(messages),
			input:              input,
			previousResponseID: previousResponseID,
		}
	}
	return planFullCodexRequest(messages, true)
}

func planFullCodexRequest(messages []agent.Message, includeReasoningItems bool) codexRequestPlan {
	return codexRequestPlan{
		mode:         codexTurnModeFullContext,
		instructions: collectCodexInstructions(messages),
		input:        codexInputItems(messages, includeReasoningItems),
	}
}

func planCodexContinuation(messages []agent.Message, previousResponseID string) codexRequestPlan {
	return codexRequestPlan{
		mode:               codexTurnModeContinuationOnly,
		instructions:       collectCodexInstructions(messages),
		input:              []map[string]any{},
		previousResponseID: strings.TrimSpace(previousResponseID),
	}
}

func codexInputItems(messages []agent.Message, includeReasoningItems bool) []map[string]any {
	input := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "" || role == "system" {
			continue
		}

		switch role {
		case "user", "assistant":
			if includeReasoningItems && role == "assistant" {
				for _, item := range codexReasoningInputItems(msg.ProviderState) {
					input = append(input, item)
				}
			}
			if item, ok := codexMessageInputItem(role, msg); ok {
				input = append(input, item)
			}
			if role == "assistant" {
				for _, call := range msg.ToolCalls {
					input = append(input, map[string]any{
						"type":      "function_call",
						"name":      call.Name,
						"arguments": normalizeArguments(call.Input),
						"call_id":   firstNonEmpty(strings.TrimSpace(call.ID), strings.TrimSpace(msg.ToolCallID)),
					})
				}
			}
		case "tool":
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": strings.TrimSpace(msg.ToolCallID),
				"output":  strings.TrimSpace(msg.Content),
			})
		}
	}
	return input
}

func planCodexIncrementalToolResults(messages []agent.Message) (string, []map[string]any, bool) {
	assistantIdx := -1
	var previousResponseID string
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			continue
		}
		state, ok := decodeCodexProviderState(msg.ProviderState)
		if !ok {
			return "", nil, false
		}
		assistantIdx = i
		previousResponseID = state.ResponseID
		break
	}
	if assistantIdx < 0 || strings.TrimSpace(previousResponseID) == "" || assistantIdx == len(messages)-1 {
		return "", nil, false
	}
	input := make([]map[string]any, 0, len(messages)-assistantIdx-1)
	for _, msg := range messages[assistantIdx+1:] {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "tool") {
			return "", nil, false
		}
		input = append(input, map[string]any{
			"type":    "function_call_output",
			"call_id": strings.TrimSpace(msg.ToolCallID),
			"output":  strings.TrimSpace(msg.Content),
		})
	}
	if len(input) == 0 {
		return "", nil, false
	}
	return previousResponseID, input, true
}

func codexReasoningInputItems(raw json.RawMessage) []map[string]any {
	state, ok := decodeCodexProviderState(raw)
	if !ok || len(state.ReasoningItems) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(state.ReasoningItems))
	for _, itemRaw := range state.ReasoningItems {
		var item map[string]any
		if len(bytes.TrimSpace(itemRaw)) == 0 {
			continue
		}
		if err := json.Unmarshal(itemRaw, &item); err != nil {
			continue
		}
		if strings.TrimSpace(fmt.Sprint(item["type"])) != "reasoning" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func marshalCodexProviderState(responseID string, reasoningItems []json.RawMessage) json.RawMessage {
	if strings.TrimSpace(responseID) == "" {
		return nil
	}
	items := make([]json.RawMessage, 0, len(reasoningItems))
	seen := map[string]struct{}{}
	for _, item := range reasoningItems {
		trimmed := bytes.TrimSpace(item)
		if len(trimmed) == 0 {
			continue
		}
		key := string(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, append(json.RawMessage(nil), trimmed...))
	}
	raw, err := json.Marshal(codexProviderState{
		Backend:        "codex",
		ResponseID:     strings.TrimSpace(responseID),
		ReasoningItems: items,
	})
	if err != nil {
		return nil
	}
	return raw
}

func decodeCodexProviderState(raw json.RawMessage) (codexProviderState, bool) {
	var state codexProviderState
	if len(bytes.TrimSpace(raw)) == 0 {
		return codexProviderState{}, false
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return codexProviderState{}, false
	}
	if strings.TrimSpace(state.Backend) != "codex" || strings.TrimSpace(state.ResponseID) == "" {
		return codexProviderState{}, false
	}
	state.ResponseID = strings.TrimSpace(state.ResponseID)
	for i := range state.ReasoningItems {
		state.ReasoningItems[i] = append(json.RawMessage(nil), bytes.TrimSpace(state.ReasoningItems[i])...)
	}
	return state, true
}
