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

func TestBudgetAddToolCalls(t *testing.T) {
	budget := Budget{ToolCallSoftLimit: 3, ToolCallHardLimit: 5}
	if warning, exhausted := budget.AddToolCalls(2); warning != "" || exhausted {
		t.Fatalf("first AddToolCalls warning=%q exhausted=%v, want no pressure", warning, exhausted)
	}
	if budget.ToolCallCount != 2 {
		t.Fatalf("ToolCallCount = %d, want 2", budget.ToolCallCount)
	}
	if warning, exhausted := budget.AddToolCalls(1); warning == "" || exhausted {
		t.Fatalf("soft AddToolCalls warning=%q exhausted=%v, want warning only", warning, exhausted)
	}
	if budget.ToolCallCount != 3 {
		t.Fatalf("ToolCallCount = %d, want 3", budget.ToolCallCount)
	}
	if warning, exhausted := budget.AddToolCalls(3); warning != "" || !exhausted {
		t.Fatalf("hard AddToolCalls warning=%q exhausted=%v, want hard stop", warning, exhausted)
	}
	if budget.ToolCallCount != 3 {
		t.Fatalf("ToolCallCount changed after hard stop = %d, want 3", budget.ToolCallCount)
	}
}

func TestBudgetAddTokenUsage(t *testing.T) {
	budget := Budget{
		InputTokenSoftLimit:  100,
		InputTokenHardLimit:  150,
		OutputTokenSoftLimit: 50,
		OutputTokenHardLimit: 75,
	}
	if warning, exhausted := budget.AddTokenUsage(40, 20); warning != "" || exhausted {
		t.Fatalf("first AddTokenUsage warning=%q exhausted=%v, want no pressure", warning, exhausted)
	}
	if budget.InputTokenCount != 40 || budget.OutputTokenCount != 20 {
		t.Fatalf("token counts = %d/%d, want 40/20", budget.InputTokenCount, budget.OutputTokenCount)
	}
	if warning, exhausted := budget.AddTokenUsage(20, 30); warning == "" || exhausted {
		t.Fatalf("soft AddTokenUsage warning=%q exhausted=%v, want warning only", warning, exhausted)
	}
	if budget.InputTokenCount != 60 || budget.OutputTokenCount != 50 {
		t.Fatalf("token counts = %d/%d, want 60/50", budget.InputTokenCount, budget.OutputTokenCount)
	}
	if warning, exhausted := budget.AddTokenUsage(0, 30); warning != "" || !exhausted {
		t.Fatalf("hard AddTokenUsage warning=%q exhausted=%v, want hard stop", warning, exhausted)
	}
}
