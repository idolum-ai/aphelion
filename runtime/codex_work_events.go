//go:build linux

package runtime

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

func codexWorkEventFromNotification(method string, params map[string]any) (session.WorkCodexEvent, bool) {
	method = strings.TrimSpace(method)
	if method == "" {
		return session.WorkCodexEvent{}, false
	}
	kind := codexWorkEventKind(method)
	if kind == "" {
		return session.WorkCodexEvent{}, false
	}
	event := session.WorkCodexEvent{
		Kind:     kind,
		Method:   method,
		Status:   firstRuntimeWorkNonEmpty(stringField(params, "status"), stringField(params, "state"), stringField(params, "phase")),
		ThreadID: firstRuntimeWorkNonEmpty(stringField(params, "threadId"), stringField(params, "thread_id"), nestedString(params, "thread", "id")),
		TurnID:   firstRuntimeWorkNonEmpty(stringField(params, "turnId"), stringField(params, "turn_id"), nestedString(params, "turn", "id")),
	}
	switch kind {
	case "file_change":
		event.Path = firstRuntimeWorkNonEmpty(
			stringField(params, "path"),
			stringField(params, "filePath"),
			stringField(params, "file_path"),
			stringField(params, "relativePath"),
			stringField(params, "relative_path"),
			stringField(params, "file"),
			nestedString(params, "file", "path"),
		)
		event.Preview = firstRuntimeWorkNonEmpty(
			stringField(params, "diff"),
			stringField(params, "patch"),
			stringField(params, "preview"),
			stringField(params, "summary"),
			stringField(params, "reason"),
		)
		event.Subject = firstRuntimeWorkNonEmpty(event.Path, stringField(params, "summary"), stringField(params, "reason"))
	case "command":
		event.Command = firstRuntimeWorkNonEmpty(
			stringField(params, "command"),
			stringField(params, "cmd"),
			stringField(params, "shellCommand"),
			stringField(params, "shell_command"),
			stringSliceField(params, "argv"),
		)
		event.Preview = firstRuntimeWorkNonEmpty(
			stringField(params, "output"),
			stringField(params, "stdout"),
			stringField(params, "stderr"),
			stringField(params, "result"),
			stringField(params, "summary"),
			stringField(params, "reason"),
		)
		if exitCode, ok := scalarField(params, "exitCode", "exit_code"); ok {
			if event.Status == "" {
				event.Status = "exit_code_" + exitCode
			} else {
				event.Status = event.Status + " exit_code_" + exitCode
			}
		}
		event.Subject = firstRuntimeWorkNonEmpty(event.Command, stringField(params, "summary"), stringField(params, "reason"))
	case "user_input":
		event.Subject = firstRuntimeWorkNonEmpty(
			stringField(params, "prompt"),
			stringField(params, "question"),
			stringField(params, "title"),
			stringField(params, "label"),
			stringField(params, "reason"),
		)
		event.Preview = firstRuntimeWorkNonEmpty(
			stringSliceField(params, "options"),
			stringField(params, "description"),
			stringField(params, "summary"),
		)
	case "subagent":
		event.AgentID = firstRuntimeWorkNonEmpty(stringField(params, "agentId"), stringField(params, "agent_id"), stringField(params, "id"))
		event.Subject = firstRuntimeWorkNonEmpty(event.AgentID, stringField(params, "name"), stringField(params, "role"), stringField(params, "summary"))
		event.Preview = firstRuntimeWorkNonEmpty(stringField(params, "name"), stringField(params, "role"), stringField(params, "summary"))
	case "mcp":
		event.Server = firstRuntimeWorkNonEmpty(stringField(params, "server"), stringField(params, "serverName"), stringField(params, "connector"))
		event.Tool = firstRuntimeWorkNonEmpty(stringField(params, "tool"), stringField(params, "toolName"), stringField(params, "name"))
		event.Subject = firstRuntimeWorkNonEmpty(joinNonEmpty("/", event.Server, event.Tool), stringField(params, "summary"))
		event.Preview = firstRuntimeWorkNonEmpty(stringField(params, "summary"), stringField(params, "result"), stringField(params, "reason"))
	case "auto_review":
		event.Subject = firstRuntimeWorkNonEmpty(stringField(params, "summary"), stringField(params, "reason"), stringField(params, "name"), event.Status)
		event.Preview = firstRuntimeWorkNonEmpty(stringField(params, "summary"), stringField(params, "result"), stringField(params, "reason"))
	case "rollout_history", "thread":
		event.Subject = firstRuntimeWorkNonEmpty(joinNonEmpty("/", event.ThreadID, event.TurnID), stringField(params, "summary"), event.Status)
		event.Preview = firstRuntimeWorkNonEmpty(stringField(params, "summary"), stringField(params, "reason"), stringField(params, "result"))
	default:
		event.Subject = firstRuntimeWorkNonEmpty(stringField(params, "summary"), stringField(params, "reason"), event.Status)
	}
	event = session.NormalizeWorkOperationMetadata(session.WorkOperationMetadata{CodexEvents: []session.WorkCodexEvent{event}}).CodexEvents[0]
	return event, true
}

func codexWorkEventFromServerRequest(method string, params map[string]any, decision codexAppServerApprovalDecision) (session.WorkCodexEvent, bool) {
	event, ok := codexWorkEventFromNotification(method, params)
	if !ok {
		return session.WorkCodexEvent{}, false
	}
	event.Status = firstRuntimeWorkNonEmpty(decision.Decision, event.Status)
	if event.Command == "" {
		event.Command = strings.TrimSpace(decision.Command)
	}
	if event.Subject == "" {
		event.Subject = firstRuntimeWorkNonEmpty(event.Path, event.Command, decision.Reason)
	}
	if event.Preview == "" {
		event.Preview = strings.TrimSpace(decision.Reason)
	}
	event = session.NormalizeWorkOperationMetadata(session.WorkOperationMetadata{CodexEvents: []session.WorkCodexEvent{event}}).CodexEvents[0]
	return event, true
}

func codexWorkEventKind(method string) string {
	compact := strings.ToLower(strings.TrimSpace(method))
	if compact == "" {
		return ""
	}
	switch {
	case strings.Contains(compact, "filechange") ||
		strings.Contains(compact, "file/change") ||
		strings.Contains(compact, "file_change") ||
		strings.Contains(compact, "patch"):
		return "file_change"
	case strings.Contains(compact, "commandexecution") ||
		strings.Contains(compact, "command/execution") ||
		strings.Contains(compact, "command_execution") ||
		strings.Contains(compact, "exec"):
		return "command"
	case strings.Contains(compact, "requestuserinput") ||
		strings.Contains(compact, "userinput") ||
		strings.Contains(compact, "user_input") ||
		strings.Contains(compact, "request/user/input"):
		return "user_input"
	case strings.Contains(compact, "spawn") ||
		strings.Contains(compact, "subagent"):
		return "subagent"
	case strings.Contains(compact, "mcp"):
		return "mcp"
	case strings.Contains(compact, "autoreview") ||
		strings.Contains(compact, "auto_review") ||
		strings.Contains(compact, "auto-review"):
		return "auto_review"
	case strings.Contains(compact, "rollout") ||
		strings.Contains(compact, "history"):
		return "rollout_history"
	case strings.Contains(compact, "thread/") ||
		strings.Contains(compact, "turn/"):
		return "thread"
	default:
		return ""
	}
}

func codexWorkAppendEvent(events []session.WorkCodexEvent, event session.WorkCodexEvent) []session.WorkCodexEvent {
	normalized := session.NormalizeWorkOperationMetadata(session.WorkOperationMetadata{CodexEvents: append(append([]session.WorkCodexEvent(nil), events...), event)}).CodexEvents
	if len(normalized) > 80 {
		return normalized[len(normalized)-80:]
	}
	return normalized
}

func codexWorkPatchPreviewFromEvents(events []session.WorkCodexEvent) string {
	var chunks []string
	for _, event := range events {
		if event.Kind != "file_change" || strings.TrimSpace(event.Preview) == "" {
			continue
		}
		label := strings.TrimSpace(event.Path)
		if label == "" {
			label = strings.TrimSpace(event.Subject)
		}
		preview := strings.TrimSpace(event.Preview)
		if label != "" {
			preview = label + ":\n" + preview
		}
		chunks = append(chunks, preview)
	}
	if len(chunks) == 0 {
		return ""
	}
	return session.NormalizeWorkOperationMetadata(session.WorkOperationMetadata{PatchPreview: strings.Join(chunks, "\n\n")}).PatchPreview
}

func codexWorkChangedFilesFromEvents(events []session.WorkCodexEvent) []string {
	var files []string
	for _, event := range events {
		if event.Kind != "file_change" {
			continue
		}
		files = append(files, firstRuntimeWorkNonEmpty(event.Path, event.Subject))
	}
	return session.NormalizeWorkOperationMetadata(session.WorkOperationMetadata{ChangedFiles: files}).ChangedFiles
}

func codexWorkCommandsFromEvents(events []session.WorkCodexEvent) []string {
	var commands []string
	for _, event := range events {
		if event.Kind != "command" {
			continue
		}
		commands = append(commands, firstRuntimeWorkNonEmpty(event.Command, event.Subject))
	}
	return session.NormalizeWorkOperationMetadata(session.WorkOperationMetadata{Commands: commands}).Commands
}

func codexWorkCommitLaneStatus(req WorkRequest, events []session.WorkCodexEvent, approvals []codexAppServerApprovalDecision) string {
	hasFileChange := len(codexWorkChangedFilesFromEvents(events)) > 0
	hasCommit := false
	for _, event := range events {
		if strings.Contains(strings.ToLower(strings.TrimSpace(firstRuntimeWorkNonEmpty(event.Command, event.Subject))), "git commit") {
			hasCommit = true
			break
		}
	}
	for _, approval := range approvals {
		if approval.Decision == "accept" && approval.Method == "item/fileChange/requestApproval" {
			hasFileChange = true
		}
		if strings.Contains(strings.ToLower(strings.TrimSpace(approval.Command)), "git commit") {
			hasCommit = true
		}
	}
	switch req.Mode {
	case WorkModeWorkspaceWrite:
		if hasFileChange {
			return "commit_requires_separate_lease"
		}
	case WorkModeCommit, WorkModeDeploy:
		if hasCommit {
			return "commit_attempted"
		}
		if hasFileChange && req.Mode == WorkModeCommit {
			return "commit_not_observed"
		}
	}
	return ""
}

func codexWorkResultFromAppServer(req WorkRequest, threadID string, turnID string, result codexAppServerResult) WorkResult {
	events := session.NormalizeWorkOperationMetadata(session.WorkOperationMetadata{CodexEvents: result.CodexEvents}).CodexEvents
	changedFiles := codexWorkChangedFilesFromEvents(events)
	commands := codexWorkCommandsFromEvents(events)
	patchPreview := firstRuntimeWorkNonEmpty(result.PatchPreview, codexWorkPatchPreviewFromEvents(events))
	return WorkResult{
		ExecutorName:     "codex",
		ThreadID:         firstRuntimeWorkNonEmpty(strings.TrimSpace(result.ThreadID), threadID),
		TurnID:           firstRuntimeWorkNonEmpty(strings.TrimSpace(result.TurnID), turnID),
		Summary:          strings.TrimSpace(result.Text),
		ChangedFiles:     changedFiles,
		Commands:         commands,
		CodexEvents:      events,
		PatchPreview:     patchPreview,
		CommitLaneStatus: codexWorkCommitLaneStatus(req, events, result.ApprovalLog),
		ApprovalLog:      append([]codexAppServerApprovalDecision(nil), result.ApprovalLog...),
		CompletionKind:   "codex_app_server",
		SideEffects:      codexApprovalLogHasSideEffects(result.ApprovalLog) || len(changedFiles) > 0,
	}
}

func stringSliceField(obj map[string]any, key string) string {
	if obj == nil {
		return ""
	}
	raw, ok := obj[key]
	if !ok || raw == nil {
		return ""
	}
	switch values := raw.(type) {
	case []string:
		return strings.Join(values, " ")
	case []any:
		parts := make([]string, 0, len(values))
		for _, value := range values {
			part := strings.TrimSpace(fmt.Sprint(value))
			if part != "" {
				parts = append(parts, part)
			}
		}
		return strings.Join(parts, " ")
	default:
		return strings.TrimSpace(fmt.Sprint(raw))
	}
}

func scalarField(obj map[string]any, keys ...string) (string, bool) {
	if obj == nil {
		return "", false
	}
	for _, key := range keys {
		if value, ok := obj[key]; ok && value != nil {
			return strings.TrimSpace(fmt.Sprint(value)), true
		}
	}
	return "", false
}

func joinNonEmpty(sep string, values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, sep)
}
