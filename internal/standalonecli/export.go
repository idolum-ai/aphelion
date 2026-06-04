//go:build linux

package standalonecli

func RunQuickstartCommand(args []string) error { return runQuickstartCommand(args) }
func RunAgencyEvalCommand(args []string) error { return runAgencyEvalCommand(args) }
func RunVersionCommand(args []string) error    { return runVersionCommand(args) }
func RunStatusCommand(args []string) error     { return runStatusCommand(args) }
