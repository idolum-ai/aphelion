//go:build linux

package agent

import "testing"

func TestBudgetTick(t *testing.T) {
	tests := []struct {
		name             string
		budget           Budget
		wantWarning      string
		wantExhausted    bool
		wantUsedAfterRun int
	}{
		{
			name: "below caution threshold",
			budget: Budget{
				Max:     10,
				Used:    5,
				Caution: 0.7,
				Warning: 0.9,
			},
			wantWarning:      "",
			wantExhausted:    false,
			wantUsedAfterRun: 6,
		},
		{
			name: "at caution threshold",
			budget: Budget{
				Max:     10,
				Used:    6,
				Caution: 0.7,
				Warning: 0.9,
			},
			wantWarning:      "You're running low on iterations. Start wrapping up.",
			wantExhausted:    false,
			wantUsedAfterRun: 7,
		},
		{
			name: "at warning threshold",
			budget: Budget{
				Max:     10,
				Used:    8,
				Caution: 0.7,
				Warning: 0.9,
			},
			wantWarning:      "⚠️ Last iteration. Return your final response now.",
			wantExhausted:    false,
			wantUsedAfterRun: 9,
		},
		{
			name: "at exhaustion threshold",
			budget: Budget{
				Max:     10,
				Used:    9,
				Caution: 0.7,
				Warning: 0.9,
			},
			wantWarning:      "",
			wantExhausted:    true,
			wantUsedAfterRun: 10,
		},
		{
			name: "beyond exhaustion threshold",
			budget: Budget{
				Max:     10,
				Used:    10,
				Caution: 0.7,
				Warning: 0.9,
			},
			wantWarning:      "",
			wantExhausted:    true,
			wantUsedAfterRun: 11,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warning, exhausted := tt.budget.Tick()

			if warning != tt.wantWarning {
				t.Fatalf("warning = %q, want %q", warning, tt.wantWarning)
			}

			if exhausted != tt.wantExhausted {
				t.Fatalf("exhausted = %v, want %v", exhausted, tt.wantExhausted)
			}

			if tt.budget.Used != tt.wantUsedAfterRun {
				t.Fatalf("Used = %d, want %d", tt.budget.Used, tt.wantUsedAfterRun)
			}
		})
	}
}
