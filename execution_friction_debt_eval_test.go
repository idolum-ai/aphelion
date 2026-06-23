//go:build linux

package main

import "testing"

func TestExecutionFrictionDebtEvalOutputExposure(t *testing.T) {
	assertExecutionFrictionDebtEvalShape(t, 1, 2, 3, 4)
}

func TestExecutionFrictionDebtEvalCausalClosure(t *testing.T) {
	assertExecutionFrictionDebtEvalShape(t, 5, 6, 7, 8, 13)
}

func TestExecutionFrictionDebtEvalTypedOperationsAndResources(t *testing.T) {
	assertExecutionFrictionDebtEvalShape(t, 9, 10, 11, 12, 14, 15, 16, 17)
}

func TestExecutionFrictionDebtEvalDurableChildProtocol(t *testing.T) {
	assertExecutionFrictionDebtEvalShape(t, 18, 19, 24)
}

func TestExecutionFrictionDebtEvalContextStatusAndReliability(t *testing.T) {
	assertExecutionFrictionDebtEvalShape(t, 20, 21, 22, 23)
}
