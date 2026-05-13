//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func inboundOriginLabel(msg core.InboundMessage) string {
	origin := strings.TrimSpace(string(msg.Origin))
	if origin == "" {
		return string(core.InboundOriginUser)
	}
	return origin
}

func inboundOriginDetailLabel(msg core.InboundMessage) string {
	return strings.TrimSpace(msg.OriginDetail)
}
