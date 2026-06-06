//go:build linux

package agent

// Budget tracks iteration and tool-call usage for an agent turn.
type Budget struct {
	Max     int
	Used    int
	Caution float64
	Warning float64

	ToolCallCount     int
	ToolCallSoftLimit int
	ToolCallHardLimit int

	InputTokenCount      int64
	OutputTokenCount     int64
	InputTokenSoftLimit  int64
	InputTokenHardLimit  int64
	OutputTokenSoftLimit int64
	OutputTokenHardLimit int64
}

const (
	defaultToolCallSoftLimit = 15
	defaultToolCallHardLimit = 30
)

// Tick consumes one iteration and reports warning text or exhaustion.
func (b *Budget) Tick() (warning string, exhausted bool) {
	b.Used++
	ratio := float64(b.Used) / float64(b.Max)

	switch {
	case ratio >= 1.0:
		return "", true
	case ratio >= b.Warning:
		return "⚠️ Last iteration. Return your final response now.", false
	case ratio >= b.Caution:
		return "You're running low on iterations. Start wrapping up.", false
	default:
		return "", false
	}
}

// AddToolCalls records model-requested tool calls and reports soft/hard budget pressure.
func (b *Budget) AddToolCalls(count int) (warning string, exhausted bool) {
	if b == nil || count <= 0 {
		return "", false
	}
	soft, hard := b.toolCallLimits()
	if hard <= 0 {
		b.ToolCallCount += count
		return "", false
	}
	if b.ToolCallCount+count > hard {
		return "", true
	}
	b.ToolCallCount += count
	if soft > 0 && b.ToolCallCount >= soft {
		return "⚠️ Tool-call budget is running high. Summarize progress and avoid nonessential tool calls.", false
	}
	return "", false
}

// AddTokenUsage records provider-reported token usage and reports token pressure.
func (b *Budget) AddTokenUsage(inputTokens int64, outputTokens int64) (warning string, exhausted bool) {
	if b == nil {
		return "", false
	}
	if inputTokens > 0 {
		b.InputTokenCount += inputTokens
	}
	if outputTokens > 0 {
		b.OutputTokenCount += outputTokens
	}
	if b.InputTokenHardLimit > 0 && b.InputTokenCount > b.InputTokenHardLimit {
		return "", true
	}
	if b.OutputTokenHardLimit > 0 && b.OutputTokenCount > b.OutputTokenHardLimit {
		return "", true
	}
	if b.OutputTokenSoftLimit > 0 && b.OutputTokenCount >= b.OutputTokenSoftLimit {
		return "⚠️ Output-token budget is running high. Keep the next response compact and avoid nonessential narration.", false
	}
	if b.InputTokenSoftLimit > 0 && b.InputTokenCount >= b.InputTokenSoftLimit {
		return "⚠️ Input-token budget is running high. Summarize progress and avoid reloading nonessential context.", false
	}
	return "", false
}

func (b *Budget) toolCallLimits() (soft int, hard int) {
	soft = b.ToolCallSoftLimit
	hard = b.ToolCallHardLimit
	if soft <= 0 {
		soft = defaultToolCallSoftLimit
	}
	if hard <= 0 {
		hard = defaultToolCallHardLimit
	}
	if hard > 0 && soft > hard {
		soft = hard
	}
	return soft, hard
}
