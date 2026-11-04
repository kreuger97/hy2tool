package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// UpdateHysteria only replaces the installed executable. It never runs the
// installer or writes configuration, certificates, firewall rules, or units.
func UpdateHysteria() error {
	url, err := updateURL(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	binary, err := exec.LookPath("hysteria")
	if err != nil {
		return fmt.Errorf("Hysteria is not installed; run hy2tool install first: %w", err)
	}
	return updateHysteria(binary, url)
}

func updateURL(goos, arch string) (string, error) {
	if goos != "linux" {
		return "", fmt.Errorf("server updates require Linux")
	}
	switch arch {
	case "amd64", "arm64", "386", "arm", "s390x", "mipsle", "riscv64":
		return "https://download.hysteria.network/app/latest/hysteria-linux-" + arch, nil
	default:
		return "", fmt.Errorf("unsupported Hysteria architecture: %s", arch)
	}
}

func updateHysteria(binary, url string) error {
	// Replace the symlink target rather than removing an administrator's symlink.
	binary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return fmt.Errorf("resolve Hysteria executable: %w", err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return err
	}
	info, err := os.Stat(binary)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Hysteria executable is not a regular file: %s", binary)
	}
	state, err := commandOutput(exec.Command("systemctl", "show", "hysteria-server.service", "--property=ActiveState", "--value"))
	if err != nil {
		return fmt.Errorf("inspect service before update: %w", err)
	}
	active := false
	switch strings.TrimSpace(state) {
	case "active":
		active = true
	case "inactive", "failed":
	default:
		return fmt.Errorf("cannot update while service state is %q; wait for it to settle", strings.TrimSpace(state))
	}
	staged, err := os.CreateTemp(filepath.Dir(binary), ".hysteria-update-*")
	if err != nil {
		return err
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)
	if err := staged.Close(); err != nil {
		return err
	}
	if err := runCommand(exec.Command("curl", "--fail", "--silent", "--show-error", "--location", "--connect-timeout", "10", "--max-time", "120", "--output", stagedPath, url)); err != nil {
		return fmt.Errorf("download Hysteria update: %w", err)
	}
	if err := os.Chmod(stagedPath, info.Mode().Perm()); err != nil {
		return err
	}
	version, err := commandOutput(exec.Command(stagedPath, "version"))
	if err != nil {
		return fmt.Errorf("validate downloaded Hysteria: %w", err)
	}
	if !regexp.MustCompile(`(?m)^Version:\s+v?2\.`).MatchString(version) {
		return fmt.Errorf("downloaded executable did not identify itself as Hysteria 2: %s", version)
	}
	// A hard link keeps the old executable available for rollback without copying
	// a running executable or creating a gap at its installed path.
	backup, err := os.CreateTemp(filepath.Dir(binary), ".hysteria-backup-*")
	if err != nil {
		return err
	}
	backupPath := backup.Name()
	if err := backup.Close(); err != nil {
		os.Remove(backupPath)
		return err
	}
	if err := os.Remove(backupPath); err != nil {
		return err
	}
	if err := os.Link(binary, backupPath); err != nil {
		return fmt.Errorf("back up executable: %w", err)
	}
	keepBackup := false
	defer func() {
		if !keepBackup {
			os.Remove(backupPath)
		}
	}()
	if err := os.Rename(stagedPath, binary); err != nil {
		return fmt.Errorf("replace executable: %w", err)
	}
	if !active {
		return nil
	}
	if err := restartUpdatedService(); err != nil {
		if rollbackErr := os.Rename(backupPath, binary); rollbackErr != nil {
			keepBackup = true
			return fmt.Errorf("update restart failed: %w\nRollback failed: %v; previous executable retained at %s", err, rollbackErr, backupPath)
		}
		if rollbackErr := restartUpdatedService(); rollbackErr != nil {
			return fmt.Errorf("update restart failed: %w\nPrevious executable restored, but service recovery failed: %v", err, rollbackErr)
		}
		return fmt.Errorf("update restart failed; previous executable restored and service restarted: %w", err)
	}
	return nil
}

func restartUpdatedService() error {
	if err := runCommand(exec.Command("systemctl", "restart", "hysteria-server.service")); err != nil {
		return err
	}
	return waitForService(2*time.Second, 200*time.Millisecond)
}
