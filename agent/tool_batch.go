//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

const (
	toolBatchModeSerial   = "serial"
	toolBatchModeParallel = "parallel"
)

type toolBatchResult struct {
	messages    []Message
	toolLog     []string
	failedCount int
}

type preparedToolCall struct {
	call       ToolCall
	requestSig string
}

type parallelToolBatchPlan struct {
	prepared                  []preparedToolCall
	parallel                  bool
	parallelEligible          bool
	parallelSafeCount         int
	parallelBlockedReason     string
	parallelMissedOpportunity bool
	parallelMissedReason      string
}

type toolExecutionResult struct {
	call       ToolCall
	requestSig string
	output     string
	err        error
}

func executeToolBatch(
	ctx context.Context,
	tools ToolRegistry,
	calls []ToolCall,
	loopGuard *toolLoopGuardState,
	pendingBudget *string,
	observer TurnObserver,
) toolBatchResult {
	mode := toolBatchModeSerial
	plan := planParallelToolBatch(tools, calls, loopGuard)
	if plan.parallel {
		mode = toolBatchModeParallel
	}

	event := ToolBatchEvent{
		Mode:                      mode,
		BatchSize:                 len(calls),
		ToolNames:                 toolBatchNames(calls),
		ParallelEligible:          plan.parallelEligible,
		ParallelSafeCount:         plan.parallelSafeCount,
		ParallelBlockedReason:     plan.parallelBlockedReason,
		ParallelMissedOpportunity: plan.parallelMissedOpportunity,
		ParallelMissedReason:      plan.parallelMissedReason,
	}
	if observer != nil {
		observer.ToolBatchStarted(ctx, event)
	}
	started := time.Now()

	var result toolBatchResult
	if plan.parallel {
		result = executeParallelToolBatch(ctx, tools, plan.prepared, loopGuard, pendingBudget)
	} else {
		result = executeSerialToolBatch(ctx, tools, calls, loopGuard, pendingBudget)
	}

	if observer != nil {
		event.Duration = time.Since(started)
		event.FailedCount = result.failedCount
		observer.ToolBatchFinished(ctx, event)
	}
	return result
}

func planParallelToolBatch(tools ToolRegistry, calls []ToolCall, loopGuard *toolLoopGuardState) parallelToolBatchPlan {
	plan := parallelToolBatchPlan{}
	if len(calls) == 0 {
		plan.parallelBlockedReason = "empty_batch"
		return plan
	}
	if len(calls) == 1 {
		plan.parallelBlockedReason = "single_call"
		plan.parallelMissedOpportunity, plan.parallelMissedReason = missedParallelOpportunityForCall(calls[0])
		return plan
	}
	if tools == nil {
		plan.parallelBlockedReason = "no_tool_registry"
		return plan
	}
	parallelSafe, ok := tools.(ParallelSafeToolRegistry)
	if !ok {
		plan.parallelBlockedReason = "registry_without_parallel_support"
		return plan
	}

	prepared := make([]preparedToolCall, 0, len(calls))
	for _, call := range calls {
		repairedInput, inputErr := repairToolInput(call.Input)
		if inputErr != nil {
			plan.parallelBlockedReason = "invalid_tool_input"
			return plan
		}
		call.Input = repairedInput
		requestSig, loopBlocked := loopGuard.shouldBlock(call)
		if loopBlocked {
			plan.parallelBlockedReason = "loop_guard"
			return plan
		}
		if !parallelSafe.SupportsParallelToolCall(call.Name, call.Input) {
			plan.parallelBlockedReason = "unsafe_tool"
			plan.parallelMissedOpportunity, plan.parallelMissedReason = missedParallelOpportunityForCall(call)
			return plan
		}
		plan.parallelSafeCount++
		prepared = append(prepared, preparedToolCall{call: call, requestSig: requestSig})
	}
	plan.prepared = prepared
	plan.parallel = true
	plan.parallelEligible = true
	return plan
}

func executeSerialToolBatch(
	ctx context.Context,
	tools ToolRegistry,
	calls []ToolCall,
	loopGuard *toolLoopGuardState,
	pendingBudget *string,
) toolBatchResult {
	result := toolBatchResult{
		messages: make([]Message, 0, len(calls)),
		toolLog:  make([]string, 0, len(calls)),
	}

	for _, call := range calls {
		repairedInput, inputErr := repairToolInput(call.Input)
		if inputErr != nil {
			content := withBudgetWarning(fmt.Sprintf("tool_error: Invalid tool arguments for %s: %v", call.Name, inputErr), pendingBudget)
			result.appendToolMessage(call, content, true)
			continue
		}
		call.Input = repairedInput

		requestSig, loopBlocked := loopGuard.shouldBlock(call)
		if loopBlocked {
			content := withBudgetWarning(fmt.Sprintf("tool_error: no-progress tool loop blocked for %s", call.Name), pendingBudget)
			result.appendToolMessage(call, content, true)
			continue
		}

		executed := executeSingleToolCall(ctx, tools, preparedToolCall{call: call, requestSig: requestSig})
		result.appendExecutedToolMessage(executed, loopGuard, pendingBudget)
	}
	return result
}

func executeParallelToolBatch(
	ctx context.Context,
	tools ToolRegistry,
	prepared []preparedToolCall,
	loopGuard *toolLoopGuardState,
	pendingBudget *string,
) toolBatchResult {
	executed := make([]toolExecutionResult, len(prepared))
	var wg sync.WaitGroup
	wg.Add(len(prepared))
	for i, call := range prepared {
		i := i
		call := call
		go func() {
			defer wg.Done()
			executed[i] = executeSingleToolCall(ctx, tools, call)
		}()
	}
	wg.Wait()

	result := toolBatchResult{
		messages: make([]Message, 0, len(executed)),
		toolLog:  make([]string, 0, len(executed)),
	}
	for _, item := range executed {
		result.appendExecutedToolMessage(item, loopGuard, pendingBudget)
	}
	return result
}

func executeSingleToolCall(ctx context.Context, tools ToolRegistry, prepared preparedToolCall) toolExecutionResult {
	call := prepared.call
	out, toolErr := tools.Execute(ctx, call.Name, call.Input)
	if toolErr != nil {
		if errors.Is(toolErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			log.Printf("INFO suppressing expected canceled tool execution tool=%s id=%s err=%v", call.Name, call.ID, toolErr)
		} else {
			log.Printf("WARN tool execution failed tool=%s id=%s err=%v", call.Name, call.ID, toolErr)
		}
	} else {
		log.Printf("INFO tool execution completed tool=%s id=%s", call.Name, call.ID)
	}
	return toolExecutionResult{call: call, requestSig: prepared.requestSig, output: out, err: toolErr}
}

func (r *toolBatchResult) appendExecutedToolMessage(executed toolExecutionResult, loopGuard *toolLoopGuardState, pendingBudget *string) {
	content, failed := toolResultContent(executed.output, executed.err)
	loopGuard.recordOutcome(executed.requestSig, content, failed)
	content = withBudgetWarning(content, pendingBudget)
	r.appendToolMessage(executed.call, content, failed)
}

func (r *toolBatchResult) appendToolMessage(call ToolCall, content string, failed bool) {
	if failed {
		r.toolLog = append(r.toolLog, fmt.Sprintf("%s:error", call.Name))
		r.failedCount++
	} else {
		r.toolLog = append(r.toolLog, fmt.Sprintf("%s:ok", call.Name))
	}
	r.messages = append(r.messages, Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: call.ID,
		ToolName:   call.Name,
	})
}

func toolResultContent(output string, err error) (string, bool) {
	if err == nil {
		return output, false
	}
	if output != "" {
		return fmt.Sprintf("tool_error: %v\n%s", err, output), true
	}
	return fmt.Sprintf("tool_error: %v", err), true
}

func toolBatchNames(calls []ToolCall) []string {
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		if name := strings.TrimSpace(call.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func missedParallelOpportunityForCall(call ToolCall) (bool, string) {
	if strings.TrimSpace(call.Name) != "exec" {
		return false, ""
	}
	command := toolCallInputString(call.Input, "command", "cmd")
	switch explorationCommandKind(command) {
	case "search":
		return true, "exec_search_could_use_search"
	case "read":
		return true, "exec_read_could_use_read_file"
	case "list":
		return true, "exec_list_could_use_list_dir"
	default:
		return false, ""
	}
}

func toolCallInputString(input json.RawMessage, keys ...string) string {
	if len(input) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(input, &payload); err != nil {
		return ""
	}
	for _, key := range keys {
		value := strings.TrimSpace(fmt.Sprint(payload[strings.TrimSpace(key)]))
		if value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func explorationCommandKind(command string) string {
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "" {
		return ""
	}
	if commandHasToken(command, "rg", "grep", "ripgrep") {
		return "search"
	}
	if commandHasToken(command, "cat", "sed", "head", "tail", "nl", "less") {
		return "read"
	}
	if commandHasToken(command, "ls", "tree") {
		return "list"
	}
	if commandHasToken(command, "find") {
		return "search"
	}
	return ""
}

func commandHasToken(command string, tokens ...string) bool {
	fields := strings.FieldsFunc(command, func(r rune) bool {
		switch r {
		case ' ', '\t', '\r', '\n', ';', '&', '|', '(', ')':
			return true
		default:
			return false
		}
	})
	if len(fields) == 0 {
		return false
	}
	want := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token != "" {
			want[token] = struct{}{}
		}
	}
	for _, field := range fields {
		field = strings.Trim(strings.TrimSpace(field), `"'`)
		if _, ok := want[field]; ok {
			return true
		}
	}
	return false
}
