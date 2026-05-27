//go:build linux

package maintenancecli

import (
	"fmt"
	"os"
	"strings"
)

func commandGroupHelpRequested(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "help", "--help", "-h":
		return true
	default:
		return false
	}
}

func printCommandGroupHelp(command string, subcommands []string) {
	fmt.Fprintf(os.Stdout, "Usage: aphelion %s <subcommand> [flags]\n\n", strings.TrimSpace(command))
	fmt.Fprintln(os.Stdout, "Subcommands:")
	for _, subcommand := range subcommands {
		fmt.Fprintf(os.Stdout, "  %s\n", subcommand)
	}
	fmt.Fprintf(os.Stdout, "\nRun 'aphelion %s <subcommand> --help' for subcommand flags.\n", strings.TrimSpace(command))
}
