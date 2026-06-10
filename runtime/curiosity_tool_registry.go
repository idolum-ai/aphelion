//go:build linux

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/idolum-ai/aphelion/agent"
)

type curiosityCandidate struct {
	ID              string          `json:"id"`
	SourceKind      string          `json:"source_kind"`
	SourceRef       string          `json:"source_ref"`
	SubjectKey      string          `json:"subject_key"`
	SignalCategory  string          `json:"signal_category"`
	SignalSummary   string          `json:"signal_summary"`
	SignalIntensity float64         `json:"signal_intensity"`
	ToolName        string          `json:"tool_name"`
	ToolInput       json.RawMessage `json:"tool_input"`
}

type curiosityToolRegistry struct {
	base      agent.ToolRegistry
	candidate curiosityCandidate

	mu         sync.Mutex
	used       bool
	outputHash string
	output     string
}

func (r *curiosityToolRegistry) Definitions() []agent.ToolDef {
	if r == nil || r.base == nil {
		return nil
	}
	want := strings.TrimSpace(r.candidate.ToolName)
	if want == "" {
		return nil
	}
	defs := r.base.Definitions()
	out := make([]agent.ToolDef, 0, 1)
	for _, def := range defs {
		if strings.TrimSpace(def.Name) == want {
			out = append(out, def)
			break
		}
	}
	return out
}

func (r *curiosityToolRegistry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	if r == nil || r.base == nil {
		return "", fmt.Errorf("curiosity tool registry unavailable")
	}
	name = strings.TrimSpace(name)
	if name == "" || name != strings.TrimSpace(r.candidate.ToolName) {
		return "", fmt.Errorf("curiosity tool %q is not the selected candidate tool", name)
	}
	if !jsonInputsEqual(input, r.candidate.ToolInput) {
		return "", fmt.Errorf("curiosity tool input does not match selected candidate")
	}
	out, err := r.base.Execute(ctx, name, input)
	if err != nil {
		return out, err
	}
	r.mu.Lock()
	r.used = true
	r.output = out
	r.outputHash = "sha256:" + shortRuntimeHash(name, string(input), out)
	r.mu.Unlock()
	return out, nil
}

func (r *curiosityToolRegistry) SupportsParallelToolCall(name string, input json.RawMessage) bool {
	if r == nil || strings.TrimSpace(name) != strings.TrimSpace(r.candidate.ToolName) || !jsonInputsEqual(input, r.candidate.ToolInput) {
		return false
	}
	parallelSafe, ok := r.base.(agent.ParallelSafeToolRegistry)
	if !ok {
		return false
	}
	return parallelSafe.SupportsParallelToolCall(name, input)
}

func (r *curiosityToolRegistry) Used() (bool, string, string) {
	if r == nil {
		return false, "", ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.used, r.outputHash, r.output
}

func jsonInputsEqual(left json.RawMessage, right json.RawMessage) bool {
	left = bytes.TrimSpace(left)
	right = bytes.TrimSpace(right)
	if len(left) == 0 || len(right) == 0 {
		return len(left) == len(right)
	}
	var leftValue any
	var rightValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		return false
	}
	if err := json.Unmarshal(right, &rightValue); err != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
