package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviousInstallation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		binary  bool
		statErr error
		want    bool
	}{
		{name: "clean server", statErr: os.ErrNotExist},
		{name: "binary present", binary: true, statErr: os.ErrNotExist, want: true},
		{name: "configuration or unit remains", want: true},
		{name: "unreadable artifact", statErr: os.ErrPermission, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := previousInstallation(func(string) (string, error) {
				if tc.binary {
					return "/bin/hysteria", nil
				}
				return "", exec.ErrNotFound
			}, func(string) (os.FileInfo, error) { return nil, tc.statErr }, []string{"artifact"})
			if got != tc.want {
				t.Fatalf("found=%t, want %t", got, tc.want)
			}
		})
	}
}

func TestRecreateServiceReplacesMainUnit(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "custom-vendor-unit")
	if err := os.WriteFile(target, []byte("original vendor unit"), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hysteria-server.service")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := writeService(dir, func(string) (string, error) { return "/usr/local/bin/hysteria", nil }, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `ExecStart="/usr/local/bin/hysteria" server --config /etc/hysteria/config.yaml`) {
		t.Fatalf("unit not recreated: %s %v", data, err)
	}
	vendor, err := os.ReadFile(target)
	if err != nil || string(vendor) != "original vendor unit" {
		t.Fatal("followed old unit symlink")
	}
}

func TestStopForReinstall(t *testing.T) {
	for _, state := range []string{"loaded", "not-found"} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "calls")
			t.Setenv("SERVICE_CALLS", log)
			t.Setenv("SERVICE_LOAD_STATE", state)
			script := `#!/bin/sh
printf '%s\n' "$*" >> "$SERVICE_CALLS"
if [ "$1" = show ]; then echo "$SERVICE_LOAD_STATE"; fi
`
			if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			err := StopForReinstall()
			if state == "loaded" && err != nil {
				t.Fatal(err)
			}
			if state == "not-found" {
				if _, ok := err.(*SkippedError); !ok {
					t.Fatalf("expected skipped stop, got %v", err)
				}
			}
			calls, _ := os.ReadFile(log)
			if strings.Contains(string(calls), "stop hysteria-server.service") != (state == "loaded") {
				t.Fatalf("unexpected calls: %s", calls)
			}
		})
	}
}

func TestRestartServiceAppliesNewConfiguration(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	t.Setenv("SERVICE_CALLS", log)
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$SERVICE_CALLS"
`
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := RestartService(); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(calls), "restart hysteria-server\n") || strings.Contains(string(calls), "\nstart hysteria-server\n") {
		t.Fatalf("did not restart service: %s %v", calls, err)
	}
}
