//go:build linux

package agent

import (
	"context"
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
	prepared, parallel := prepareParallelToolBatch(tools, calls, loopGuard)
	if parallel {
		mode = toolBatchModeParallel
	}

	event := ToolBatchEvent{
		Mode:      mode,
		BatchSize: len(calls),
		ToolNames: toolBatchNames(calls),
	}
	if observer != nil {
		observer.ToolBatchStarted(ctx, event)
	}
	started := time.Now()

	var result toolBatchResult
	if parallel {
		result = executeParallelToolBatch(ctx, tools, prepared, loopGuard, pendingBudget)
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

func prepareParallelToolBatch(tools ToolRegistry, calls []ToolCall, loopGuard *toolLoopGuardState) ([]preparedToolCall, bool) {
	if len(calls) <= 1 || tools == nil {
		return nil, false
	}
	parallelSafe, ok := tools.(ParallelSafeToolRegistry)
	if !ok {
		return nil, false
	}

	prepared := make([]preparedToolCall, 0, len(calls))
	for _, call := range calls {
		repairedInput, inputErr := repairToolInput(call.Input)
		if inputErr != nil {
			return nil, false
		}
		call.Input = repairedInput
		requestSig, loopBlocked := loopGuard.shouldBlock(call)
		if loopBlocked {
			return nil, false
		}
		if !parallelSafe.SupportsParallelToolCall(call.Name, call.Input) {
			return nil, false
		}
		prepared = append(prepared, preparedToolCall{call: call, requestSig: requestSig})
	}
	return prepared, true
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
