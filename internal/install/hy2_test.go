package install

import (
	"errors"
	"fmt"
	"os/exec"

	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kreuger97/hy2tool/internal/config"
)

func TestGenerateCert(t *testing.T) {
	dir := t.TempDir()
	if err := generateCert(dir); err != nil {
		t.Fatalf("GenerateCert failed: %v", err)
	}
	for _, f := range []string{filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")} {
		if _, err := os.Stat(f); os.IsNotExist(err) {
			t.Fatalf("%s not created", f)
		}
	}
	t.Log("certificates created")
}

func TestWriteConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Port = "8443"
	cfg.Password = "test-password"

	dir := t.TempDir()
	if err := writeConfig(cfg, dir); err != nil {
		t.Fatalf("WriteConfig failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("cannot read config: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, ":8443") {
		t.Error("config missing port 8443")
	}
	if !strings.Contains(content, "test-password") {
		t.Error("config missing password")
	}
	if strings.Contains(content, "masquerade:") {
		t.Error("config should not contain masquerade when disabled")
	}
	t.Logf("config:\n%s", content)
}

func TestWriteConfigWithMasquerade(t *testing.T) {
	cfg := config.Default()
	cfg.Port = "443"
	cfg.Password = "test-pass"
	cfg.MasqueradeEnabled = true
	cfg.MasqueradeURL = "https://example.com"
	cfg.MasqueradeHTTPPort = ":80"
	cfg.MasqueradeHTTPSPort = ":443"
	cfg.MasqueradeForceHTTPS = true
	cfg.BandwidthUp = "50 mbps"
	cfg.BandwidthDown = "100 mbps"

	dir := t.TempDir()
	if err := writeConfig(cfg, dir); err != nil {
		t.Fatalf("WriteConfig failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("cannot read config: %v", err)
	}

	content := string(data)
	checks := []struct {
		name, want string
	}{
		{"masquerade block", "masquerade:"},
		{"proxy type", "type: proxy"},
		{"proxy url", "url: https://example.com"},
		{"rewriteHost", "rewriteHost: true"},
		{"listenHTTP", "listenHTTP: :80"},
		{"listenHTTPS", "listenHTTPS: :443"},
		{"forceHTTPS", "forceHTTPS: true"},
		{"bandwidth up", "up: 50 mbps"},
		{"bandwidth down", "down: 100 mbps"},
	}
	for _, c := range checks {
		if !strings.Contains(content, c.want) {
			t.Errorf("config missing %s (%s)", c.name, c.want)
		}
	}
	t.Logf("config:\n%s", content)
}

func TestCommandFailureDetails(t *testing.T) {
	err := runCommand(exec.Command("sh", "-c", "printf output; printf problem >&2; exit 7"))
	var failure *CommandError
	if !errors.As(err, &failure) {
		t.Fatalf("expected CommandError, got %v", err)
	}
	if failure.ExitCode != 7 || failure.Stdout != "output" || failure.Stderr != "problem" {
		t.Fatalf("incorrect details: %+v", failure)
	}
	for _, want := range []string{"Command:", "Exit code: 7", "stdout:\noutput", "stderr:\nproblem"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q: %v", want, err)
		}
	}
	if err := runCommand(exec.Command(filepath.Join(t.TempDir(), "missing"))); !errors.As(err, &failure) || failure.ExitCode != -1 {
		t.Fatalf("missing executable: %v", err)
	}
}

func TestServiceFailureDiagnostics(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	t.Setenv("CALL_LOG", log)
	for name, script := range map[string]string{
		"systemctl": `#!/bin/sh
printf '%s\n' "$*" >> "$CALL_LOG"
case "$1" in
 start) echo startup-output; echo startup-error >&2; exit 5;;
 status) echo 'Active: failed'; exit 3;;
esac
`,
		"journalctl": "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CALL_LOG\"\necho journal-details\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	err := StartService()
	if err == nil {
		t.Fatal("expected startup failure")
	}
	for _, want := range []string{"Exit code: 5", "startup-output", "startup-error", "Active: failed", "journal-details"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q: %v", want, err)
		}
	}
	calls, readErr := os.ReadFile(log)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(calls), "-u hysteria-server -n 40 --no-pager") || strings.Contains(string(calls), "enable") {
		t.Fatalf("unexpected calls: %s", calls)
	}
}

func TestInactiveServiceAndBrokenDiagnostics(t *testing.T) {
	dir := t.TempDir()
	for name, script := range map[string]string{
		"systemctl":  "#!/bin/sh\ncase \"$1\" in\nis-active) echo inactive; exit 3;;\nstatus) echo status-unavailable >&2; exit 4;;\nesac\n",
		"journalctl": "#!/bin/sh\necho journal-unavailable >&2\nexit 6\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	err := StartService()
	if err == nil {
		t.Fatal("inactive service accepted")
	}
	for _, want := range []string{"is-active", "Exit code: 3", "inactive", "status-unavailable", "journal-unavailable", "Diagnostic error: exit status 4", "Diagnostic error: exit status 6"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q: %v", want, err)
		}
	}
}

func TestEnsureService(t *testing.T) {
	for _, tc := range []struct {
		name, state                                   string
		exit                                          int
		existing, missingBinary, wantErr, wantCreated bool
	}{
		{name: "missing", state: "not-found", wantCreated: true},
		{name: "missing with nonzero status", state: "not-found", exit: 1, wantCreated: true},
		{name: "vendor unit", state: "loaded"},
		{name: "masked unit", state: "masked"},
		{name: "invalid existing unit", state: "error"},
		{name: "existing local unit", state: "not-found", existing: true},
		{name: "systemd unavailable", exit: 1, wantErr: true},
		{name: "empty response", wantErr: true},
		{name: "unexpected inspection failure", state: "not-found", exit: 127, wantErr: true},
		{name: "missing binary", state: "not-found", missingBinary: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binDir, unitDir := t.TempDir(), t.TempDir()
			script := fmt.Sprintf("#!/bin/sh\necho '%s'\necho diagnostic >&2\nexit %d\n", tc.state, tc.exit)
			if err := os.WriteFile(filepath.Join(binDir, "systemctl"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			path := filepath.Join(unitDir, "hysteria-server.service")
			if tc.existing {
				if err := os.WriteFile(path, []byte("custom unit"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			binary := filepath.Join(binDir, "custom hysteria%test")
			err := ensureService(unitDir, func(name string) (string, error) {
				if name != "hysteria" {
					t.Fatalf("unexpected lookup: %s", name)
				}
				if tc.missingBinary {
					return "", exec.ErrNotFound
				}
				return binary, nil
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr = %t", err, tc.wantErr)
			}
			data, readErr := os.ReadFile(path)
			switch {
			case tc.existing:
				if readErr != nil || string(data) != "custom unit" {
					t.Fatal("existing unit overwritten")
				}
			case tc.wantCreated:
				if readErr != nil {
					t.Fatal(readErr)
				}
				for _, want := range []string{`ExecStart="` + strings.ReplaceAll(binary, "%", "%%") + `" server --config /etc/hysteria/config.yaml`, "DynamicUser=yes", "AmbientCapabilities=CAP_NET_BIND_SERVICE", "WantedBy=multi-user.target", "Restart=on-failure"} {
					if !strings.Contains(string(data), want) {
						t.Errorf("missing %q in unit: %s", want, data)
					}
				}
			default:
				if !os.IsNotExist(readErr) {
					t.Fatalf("unexpected unit: %s, error %v", data, readErr)
				}
			}
		})
	}
}

func TestEnsureServicePreservesMaskSymlink(t *testing.T) {
	dir, binDir := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "hysteria-server.service")
	if err := os.Symlink("/dev/null", path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "systemctl"), []byte("#!/bin/sh\necho not-found\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := ensureService(dir, func(string) (string, error) { return "/usr/local/bin/hysteria", nil }); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(path)
	if err != nil || target != "/dev/null" {
		t.Fatalf("mask changed: %q %v", target, err)
	}
}

func TestServiceMustStayActive(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STATE_FILE", filepath.Join(dir, "state"))
	script := `#!/bin/sh
if [ -f "$STATE_FILE" ]; then echo failed >&2; exit 3; fi
: > "$STATE_FILE"
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	err := waitForService(100*time.Millisecond, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "Exit code: 3") {
		t.Fatalf("accepted service that immediately failed: %v", err)
	}
}
