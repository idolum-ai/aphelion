//go:build linux

package runtime

import (
	"encoding/json"
	"strings"

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/session"
)

func effectAttemptSubjectJSON(command string) string {
	rawCommand := strings.TrimSpace(command)
	command = commandeffect.NormalizeCommand(rawCommand)
	subject := map[string]any{
		"command_hash": session.EffectAttemptCommandHash(command),
	}
	effect := commandeffect.Classify(rawCommand)
	if effect.Command != "" {
		subject["command_root"] = effect.Command
	}
	if effect.GitSubcommand != "" {
		subject["git_subcommand"] = effect.GitSubcommand
	}
	tokens := strings.Fields(command)
	if len(tokens) > 0 {
		switch {
		case effect.Command == "git" && effect.GitSubcommand == "push":
			if remote, ref := gitPushSubject(tokens); remote != "" || ref != "" {
				subject["remote"] = remote
				subject["ref"] = ref
			}
		case effect.Command == "git" && effect.GitSubcommand == "commit":
			subject["kind"] = "git_commit"
		case effect.Command == "gh":
			if pr := ghPRSubject(tokens); len(pr) > 0 {
				for k, v := range pr {
					subject[k] = v
				}
			}
		case effect.Kind == commandeffect.KindService:
			if len(tokens) > 1 {
				subject["action"] = tokens[1]
			}
			if len(tokens) > 2 {
				subject["target"] = tokens[len(tokens)-1]
			}
		}
	}
	raw, err := json.Marshal(subject)
	if err != nil {
		return "{}"
	}
	return session.RedactEvidenceText(string(raw)).Text
}

func gitPushSubject(tokens []string) (string, string) {
	subcmd := false
	var positional []string
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" || strings.HasPrefix(token, "-") {
			continue
		}
		if !subcmd {
			if token == "push" {
				subcmd = true
			}
			continue
		}
		positional = append(positional, token)
	}
	if len(positional) == 0 {
		return "", ""
	}
	remote := positional[0]
	ref := ""
	if len(positional) > 1 {
		ref = positional[1]
	}
	return remote, ref
}

func ghPRSubject(tokens []string) map[string]any {
	out := map[string]any{}
	for i := 0; i < len(tokens); i++ {
		switch tokens[i] {
		case "pr":
			out["resource"] = "pull_request"
			if i+1 < len(tokens) {
				out["action"] = tokens[i+1]
			}
		case "--base", "-B":
			if i+1 < len(tokens) {
				out["base"] = tokens[i+1]
				i++
			}
		case "--head", "-H":
			if i+1 < len(tokens) {
				out["head"] = tokens[i+1]
				i++
			}
		case "--title", "-t":
			if i+1 < len(tokens) {
				out["title"] = tokens[i+1]
				i++
			}
		}
	}
	return out
}
