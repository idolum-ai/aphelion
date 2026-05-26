//go:build linux

package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func codexThreadStartParams(req codexAppServerRequest) map[string]any {
	return map[string]any{
		"baseInstructions":      codexAppServerBaseInstructions(req.Agent),
		"developerInstructions": codexAppServerDeveloperInstructions(req.Agent),
		"approvalPolicy":        "on-request",
		"sandbox":               "read-only",
		"serviceName":           "aphelion-durable-child",
		"cwd":                   "/",
	}
}

func codexThreadResumeParams() map[string]any {
	return map[string]any{
		"approvalPolicy": "on-request",
		"sandbox":        "read-only",
	}
}

func codexTurnStartParams() map[string]any {
	return map[string]any{
		"approvalPolicy": "on-request",
		"sandbox":        "read-only",
	}
}

func codexAppServerBaseInstructions(agent core.DurableAgent) string {
	return strings.TrimSpace(fmt.Sprintf(`## Role
You are a durable child runtime reachable through a Codex app-server channel.

## Goal
Report read-only status for durable agent %s inside the current parent-approved charter.

## Success Criteria
- Operate only inside the current parent-approved charter.
- For status tasks, return only the requested durable_child_status JSON object and no prose.

## Stop Rules
- Never modify files, open apps, kill processes, inspect private content, take screenshots, use Accessibility, read command-line arguments, control the UI, send messages, or manipulate the machine.`, strings.TrimSpace(agent.AgentID)))
}

func codexAppServerDeveloperInstructions(agent core.DurableAgent) string {
	charter := strings.TrimSpace(agent.LivePolicy.Charter)
	if charter == "" {
		charter = "Read-only status reporting only."
	}
	return strings.TrimSpace(fmt.Sprintf(`## Charter
%s

## Boundary
- read-only status/heartbeat tasks only
- process names only, never command-line arguments or paths
- no screenshots, UI control, Accessibility, app manipulation, file writes, process killing, messages, browser/window inspection, or private content inspection
- if a field cannot be collected safely, use an empty array or null and explain inside payload.collection_notes`, charter))
}

func codexAppServerStatusPrompt(agent core.DurableAgent, now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	agentID := strings.TrimSpace(agent.AgentID)
	displayName := codexAppServerDisplayName(agent)
	return strings.TrimSpace(fmt.Sprintf(`## Role
You are collecting one read-only durable child status heartbeat.

## Goal
Produce a single durable_child_status JSON object and nothing else.

## Success Criteria
- The response is valid JSON matching the envelope below.
- The capability_posture is read_only.
- Process entries include process names only, not command lines or paths.
- Unsafe or unavailable fields are empty arrays or null, with a short explanation in payload.collection_notes when useful.

## Stop Rules
- Use only read-only shell commands if commands are needed.
- Do not modify files.
- Do not open apps.
- Do not kill processes.
- Do not inspect private content.
- Do not take screenshots.
- Do not use Accessibility.
- Do not read command-line arguments.
- Do not control the UI.

## Output
Return this exact generic envelope shape. Include payload_hash only if you can compute the exact sha256 of the compact JSON payload; otherwise omit payload_hash:
{
  "kind": "durable_child_status",
  "agent_id": %s,
  "schema_version": "durable_child_status.v1",
  "generated_at": "%s",
  "capability_posture": "read_only",
  "payload": {
    "display_name": %s,
    "mode": "read_only",
    "machine": {
      "hostname": "...",
      "os": "macOS",
      "os_version": "...",
      "arch": "...",
      "uptime": "...",
      "disk_free_root": "..."
    },
    "top_processes": {
      "by_cpu": ["name1", "name2", "name3", "name4", "name5"],
      "by_memory": ["name1", "name2", "name3", "name4", "name5"],
      "privacy": {
        "process_names_only": true,
        "cmdline_redacted": true,
        "paths_redacted": true
      }
    },
    "capability_limits": {
      "no_screenshots": true,
      "no_typing": true,
      "no_file_operations": true,
      "no_process_killing": true,
      "no_window_control": true,
      "no_messages": true,
      "no_full_command_line_inspection": true
    }
  }
}`, jsonString(agentID), now.UTC().Format(time.RFC3339), jsonString(displayName)))
}

func codexAppServerDisplayName(agent core.DurableAgent) string {
	if value := strings.TrimSpace(agent.AgentID); value != "" {
		return value
	}
	return "durable child"
}

func jsonString(value string) string {
	raw, err := json.Marshal(strings.TrimSpace(value))
	if err != nil {
		return `""`
	}
	return string(raw)
}
