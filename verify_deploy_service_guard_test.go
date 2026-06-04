//go:build linux

package main

import (
	"context"

	"github.com/idolum-ai/aphelion/internal/standalonecli"
)

func init() {
	deployVerificationServiceGuard = func(context.Context, standalonecli.ServiceGuardCheck) (standalonecli.ServiceGuardReport, error) {
		return standalonecli.ServiceGuardReport{}, nil
	}
}
