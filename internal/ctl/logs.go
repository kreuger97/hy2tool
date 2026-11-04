package ctl

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
)

const LogsUsage = `Usage: hy2tool logs [options]

Show the journal for hysteria-server.service.

Options:
  -n, --lines N   Show the last N records (default: 40; 0: no history)
  -f, --follow    Stream new records until Ctrl+C
  -h, --help      Show this help

Examples:
  hy2tool logs
  hy2tool logs -n 100
  hy2tool logs -n 100 -f
  hy2tool logs -n 0 -f
`

func logsArgs(args []string) ([]string, error) {
	flags := flag.NewFlagSet("logs", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var lines int
	var follow bool
	flags.IntVar(&lines, "n", 40, "number of recent records")
	flags.IntVar(&lines, "lines", 40, "number of recent records")
	flags.BoolVar(&follow, "f", false, "follow journal")
	flags.BoolVar(&follow, "follow", false, "follow journal")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("unexpected logs argument: %s", flags.Arg(0))
	}
	if lines < 0 {
		return nil, fmt.Errorf("number of journal records must be non-negative")
	}
	commandArgs := []string{"--unit", "hysteria-server.service", "--no-pager", "--lines", strconv.Itoa(lines)}
	if follow {
		commandArgs = append(commandArgs, "--follow")
	}
	return commandArgs, nil
}

// CmdLogs connects journalctl directly to the terminal so follow mode streams
// immediately and terminal signals reach journalctl normally.
func CmdLogs(args []string) error {
	commandArgs, err := logsArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Print(LogsUsage)
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w (see hy2tool logs --help)", err)
	}
	cmd := exec.Command("journalctl", commandArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("journalctl: %w", err)
	}
	return nil
}
