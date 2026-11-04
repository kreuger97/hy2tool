package install

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func PreviousInstallation() bool {
	return previousInstallation(exec.LookPath, os.Lstat, []string{
		"/usr/local/bin/hysteria", "/usr/bin/hysteria",
		"/etc/hysteria/config.yaml", "/etc/ssl/hysteria/cert.pem", "/etc/ssl/hysteria/key.pem",
		"/etc/systemd/system/hysteria-server.service", "/run/systemd/system/hysteria-server.service",
		"/usr/lib/systemd/system/hysteria-server.service", "/lib/systemd/system/hysteria-server.service",
	})
}

func previousInstallation(lookPath func(string) (string, error), stat func(string) (os.FileInfo, error), paths []string) bool {
	if _, err := lookPath("hysteria"); err == nil {
		return true
	}
	for _, path := range paths {
		// An unreadable artifact is still evidence of an existing installation.
		if _, err := stat(path); !os.IsNotExist(err) {
			return true
		}
	}
	return false
}

func StopForReinstall() error {
	out, err := commandOutput(exec.Command("systemctl", "show", "hysteria-server.service", "--property=LoadState", "--value"))
	state := strings.TrimSpace(out)
	if state == "not-found" {
		var failure *CommandError
		if err == nil || errors.As(err, &failure) && failure.ExitCode == 1 {
			return &SkippedError{Reason: "previous systemd service not found"}
		}
	}
	if err != nil {
		return fmt.Errorf("inspect previous service: %w", err)
	}
	if state == "" {
		return fmt.Errorf("empty LoadState for previous service")
	}
	return runCommand(exec.Command("systemctl", "stop", "hysteria-server.service"))
}
