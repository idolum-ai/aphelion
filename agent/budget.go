//go:build linux

package agent

// Budget tracks iteration usage for an agent turn.
type Budget struct {
	Max     int
	Used    int
	Caution float64
	Warning float64
}

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
