package install

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kreuger97/hy2tool/internal/config"
)

// CommandError retains separate streams even after the TUI restores the terminal.
type CommandError struct {
	Command        string
	ExitCode       int
	Stdout, Stderr string
	Cause          error
	Diagnostics    string
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("Command: %s\nExit code: %d\nError: %v\nstdout:\n%s\nstderr:\n%s%s", e.Command, e.ExitCode, e.Cause, e.Stdout, e.Stderr, e.Diagnostics)
}
func (e *CommandError) Unwrap() error { return e.Cause }

func runCommand(cmd *exec.Cmd) error {
	_, err := commandOutput(cmd)
	return err
}

func commandOutput(cmd *exec.Cmd) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		code := -1 // The process could not start or was terminated by a signal.
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		args := make([]string, len(cmd.Args))
		for i, arg := range cmd.Args {
			args[i] = fmt.Sprintf("%q", arg)
		}
		return stdout.String(), &CommandError{Command: strings.Join(args, " "), ExitCode: code, Stdout: stdout.String(), Stderr: stderr.String(), Cause: err}
	}
	return stdout.String(), nil
}

func HysteriaInstalled() bool {
	_, err := exec.LookPath("hysteria")
	return err == nil
}

func InstallHysteria() error { return installHysteria(false) }

func ReinstallHysteria() error { return installHysteria(true) }

func installHysteria(force bool) error {
	script, err := os.CreateTemp("", "hy2-install-*.sh")
	if err != nil {
		return fmt.Errorf("create installer script: %w", err)
	}
	path := script.Name()
	defer os.Remove(path)
	if err := script.Close(); err != nil {
		return fmt.Errorf("close installer script: %w", err)
	}
	// Download must succeed before bash can read any part of the script.
	if err := runCommand(exec.Command("curl", "-fsSL", "--proto", "=https", "--proto-redir", "=https", "--connect-timeout", "10", "--max-time", "120", "-o", path, "https://get.hy2.sh/")); err != nil {
		return fmt.Errorf("download installer: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect installer script: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("downloaded installer script is empty")
	}
	args := []string{path}
	if force {
		args = append(args, "--force")
	}
	if err := runCommand(exec.Command("bash", args...)); err != nil {
		return fmt.Errorf("run installer: %w", err)
	}
	return nil
}

func GenerateCert() error {
	return generateCert("/etc/ssl/hysteria")
}

func generateCert(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	cmd := exec.Command("openssl", "req", "-x509", "-nodes", "-newkey", "rsa:4096",
		"-keyout", filepath.Join(dir, "key.pem"),
		"-out", filepath.Join(dir, "cert.pem"),
		"-days", "3650",
		"-subj", "/CN=hy2-server",
	)
	if err := runCommand(cmd); err != nil {
		return fmt.Errorf("openssl: %w", err)
	}

	for _, f := range []string{"key.pem", "cert.pem"} {
		if err := runCommand(exec.Command("chmod", "644", filepath.Join(dir, f))); err != nil {
			return fmt.Errorf("chmod %s: %w", f, err)
		}
	}
	return nil
}

func WriteConfig(cfg config.Config) error {
	return writeConfig(cfg, "/etc/hysteria")
}

func writeConfig(cfg config.Config, dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	yaml := fmt.Sprintf(`listen: :%s

auth:
  type: password
  password: %s

tls:
  cert: %s
  key: %s
`, cfg.Port, cfg.Password, cfg.CertPath, cfg.KeyPath)

	if cfg.MasqueradeEnabled {
		forceHTTPS := "false"
		if cfg.MasqueradeForceHTTPS {
			forceHTTPS = "true"
		}
		yaml += fmt.Sprintf(`
masquerade:
  type: proxy
  proxy:
    url: %s
    rewriteHost: true
  listenHTTP: %s
  listenHTTPS: %s
  forceHTTPS: %s
`, cfg.MasqueradeURL, cfg.MasqueradeHTTPPort, cfg.MasqueradeHTTPSPort, forceHTTPS)
	}

	if cfg.BandwidthUp != "" || cfg.BandwidthDown != "" {
		yaml += "\nbandwidth:\n"
		if cfg.BandwidthUp != "" {
			yaml += fmt.Sprintf("  up: %s\n", cfg.BandwidthUp)
		}
		if cfg.BandwidthDown != "" {
			yaml += fmt.Sprintf("  down: %s\n", cfg.BandwidthDown)
		}
	}

	return os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0644)
}

// EnsureService preserves installed units (including administrator overrides and masks).
// Only a unit explicitly reported missing by systemd is created.
func EnsureService() error {
	return ensureService("/etc/systemd/system", exec.LookPath)
}

func RecreateService() error { return writeService("/etc/systemd/system", exec.LookPath, true) }

func ensureService(dir string, lookPath func(string) (string, error)) error {
	return writeService(dir, lookPath, false)
}

func writeService(dir string, lookPath func(string) (string, error), replace bool) error {
	if !replace {
		out, err := commandOutput(exec.Command("systemctl", "show", "hysteria-server.service", "--property=LoadState", "--value"))
		state := strings.TrimSpace(out)
		if err != nil {
			var failure *CommandError
			if state != "not-found" || !errors.As(err, &failure) || failure.ExitCode != 1 {
				return fmt.Errorf("inspect Hysteria service: %w", err)
			}
		}
		if state != "not-found" {
			if state == "" {
				return fmt.Errorf("systemd returned an empty LoadState for hysteria-server.service")
			}
			return nil
		}
	}
	binary, err := lookPath("hysteria")
	if err != nil {
		return fmt.Errorf("find Hysteria binary for service: %w", err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return fmt.Errorf("resolve Hysteria binary: %w", err)
	}
	// systemd expands specifiers and environment variables even in quoted arguments.
	binary = strings.NewReplacer("%", "%%", "$", "$$").Replace(binary)
	unit := fmt.Sprintf(`[Unit]
Description=Hysteria 2 Server
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
DynamicUser=yes
StateDirectory=hysteria
WorkingDirectory=/var/lib/hysteria
ExecStart=%s server --config /etc/hysteria/config.yaml
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
Restart=on-failure
RestartSec=5s
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`, strconv.Quote(binary))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create systemd directory: %w", err)
	}
	path := filepath.Join(dir, "hysteria-server.service")
	if replace {
		temp, err := os.CreateTemp(dir, ".hysteria-service-*")
		if err != nil {
			return err
		}
		defer os.Remove(temp.Name())
		_, writeErr := temp.WriteString(unit)
		closeErr := temp.Close()
		if writeErr != nil {
			return fmt.Errorf("write replacement service: %w", writeErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if err := os.Chmod(temp.Name(), 0644); err != nil {
			return err
		}
		return os.Rename(temp.Name(), path)
	}
	// O_EXCL also protects existing symlinks, masks, and units created concurrently.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create Hysteria service: %w", err)
	}
	_, writeErr := f.WriteString(unit)
	closeErr := f.Close()
	if writeErr != nil {
		os.Remove(path)
		return fmt.Errorf("write Hysteria service: %w", writeErr)
	}
	if closeErr != nil {
		os.Remove(path)
		return fmt.Errorf("close Hysteria service: %w", closeErr)
	}
	return nil
}

func StartService() error   { return startService("start") }
func RestartService() error { return startService("restart") }

func startService(action string) error {
	cmds := []string{
		"systemctl daemon-reload",
		"systemctl " + action + " hysteria-server",
		"systemctl is-active --quiet hysteria-server",
		"systemctl enable hysteria-server",
	}
	for _, c := range cmds {
		var err error
		if c == "systemctl is-active --quiet hysteria-server" {
			err = waitForService(2*time.Second, 200*time.Millisecond)
		} else {
			err = runCommand(exec.Command("bash", "-c", c))
		}
		if err != nil {
			if failure, ok := err.(*CommandError); ok {
				for _, diagnostic := range [][]string{
					{"systemctl", "status", "hysteria-server", "--no-pager", "--full"},
					{"journalctl", "-u", "hysteria-server", "-n", "40", "--no-pager"},
				} {
					check := exec.Command(diagnostic[0], diagnostic[1:]...)
					var out, stderr bytes.Buffer
					check.Stdout, check.Stderr = &out, &stderr
					checkErr := check.Run()
					failure.Diagnostics += fmt.Sprintf("\n%s:\nstdout:\n%s\nstderr:\n%s\n", strings.Join(diagnostic, " "), out.String(), stderr.String())
					if checkErr != nil {
						failure.Diagnostics += fmt.Sprintf("Diagnostic error: %v\n", checkErr)
					}
				}
			}
			return err
		}
	}
	return nil
}

// Type=simple may report active before a process rejects its configuration.
// Require a short period of continuous activity before declaring success.
func waitForService(stableFor, interval time.Duration) error {
	var deadline time.Time
	for {
		if err := runCommand(exec.Command("systemctl", "is-active", "--quiet", "hysteria-server")); err != nil {
			return err
		}
		if deadline.IsZero() {
			deadline = time.Now().Add(stableFor)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil
		}
		delay := interval
		if remaining < delay {
			delay = remaining
		}
		time.Sleep(delay)
	}
}

// SkippedError reports an optional step requiring manual action.
type SkippedError struct{ Reason string }

func (e *SkippedError) Error() string { return e.Reason }

func firewallPorts(cfg config.Config) []string {
	ports := []string{cfg.Port + "/udp"}
	if cfg.MasqueradeEnabled {
		for _, listener := range []string{cfg.MasqueradeHTTPPort, cfg.MasqueradeHTTPSPort} {
			if listener != "" {
				ports = append(ports, strings.TrimPrefix(listener, ":")+"/tcp")
			}
		}
	}
	return ports
}

func ConfigureFirewall(cfg config.Config) error {
	ports := firewallPorts(cfg)
	if _, err := exec.LookPath("ufw"); err != nil {
		return &SkippedError{Reason: "ufw unavailable; manual firewall configuration required: allow inbound " + strings.Join(ports, ", ")}
	}
	for _, port := range ports {
		if err := runCommand(exec.Command("ufw", "allow", port)); err != nil {
			return err
		}
	}
	return nil
}
