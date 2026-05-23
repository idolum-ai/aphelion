//go:build linux

package main

import "github.com/idolum-ai/aphelion/internal/standalonecli"

func runQuickstartCommand(args []string) error { return standalonecli.RunQuickstartCommand(args) }
func runAgencyEvalCommand(args []string) error { return standalonecli.RunAgencyEvalCommand(args) }
func runVersionCommand(args []string) error    { return standalonecli.RunVersionCommand(args) }
