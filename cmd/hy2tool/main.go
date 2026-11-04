package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kreuger97/hy2tool/internal/ctl"
	"github.com/kreuger97/hy2tool/internal/ui"
)

func main() {
	if os.Geteuid() != 0 {
		fmt.Println("This installer requires root privileges. Please run with sudo.")
		os.Exit(1)
	}

	if len(os.Args) < 2 {
		fmt.Print(ctl.Usage)
		return
	}

	switch os.Args[1] {
	case "install":
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		if err := ui.RunWithSignals(sigCh); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	case "update":
		if err := ctl.CmdUpdate(); err != nil {
			fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
			os.Exit(1)
		}
	case "logs":
		if err := ctl.CmdLogs(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Logs failed: %v\n", err)
			os.Exit(1)
		}
	case "link":
		ctl.CmdLink()
	case "status":
		ctl.CmdStatus()
	case "start":
		ctl.CmdService("start")
	case "stop":
		ctl.CmdService("stop")
	case "restart":
		ctl.CmdService("restart")
	case "help", "-h", "--help":
		fmt.Print(ctl.Usage)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		fmt.Print(ctl.Usage)
		os.Exit(1)
	}
}
