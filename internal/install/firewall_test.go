package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kreuger97/hy2tool/internal/config"
)

func TestConfigureFirewall(t *testing.T) {
	for _, tc := range []struct {
		name              string
		enabled           bool
		http, https, want string
	}{
		{name: "Hysteria only", want: "allow 8443/udp\n"},
		{name: "default masquerade", enabled: true, http: ":80", https: ":443", want: "allow 8443/udp\nallow 80/tcp\nallow 443/tcp\n"},
		{name: "custom ports without colon", enabled: true, http: "8080", https: "8443", want: "allow 8443/udp\nallow 8080/tcp\nallow 8443/tcp\n"},
		{name: "HTTPS only", enabled: true, https: ":443", want: "allow 8443/udp\nallow 443/tcp\n"},
		{name: "HTTP only", enabled: true, http: ":80", want: "allow 8443/udp\nallow 80/tcp\n"},
		{name: "no masquerade listeners", enabled: true, want: "allow 8443/udp\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "calls")
			t.Setenv("UFW_LOG", log)
			stub := `#!/bin/sh
printf '%s\n' "$*" >> "$UFW_LOG"
if [ "$#" != 2 ] || [ "$1" != allow ]; then echo invalid-ufw-syntax >&2; exit 1; fi
case "$2" in */tcp|*/udp) exit 0;; *) echo missing-protocol >&2; exit 1;; esac
`
			if err := os.WriteFile(filepath.Join(dir, "ufw"), []byte(stub), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			cfg := config.Default()
			cfg.Port = "8443"
			cfg.MasqueradeEnabled = tc.enabled
			cfg.MasqueradeHTTPPort = tc.http
			cfg.MasqueradeHTTPSPort = tc.https
			if err := ConfigureFirewall(cfg); err != nil {
				t.Fatal(err)
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if string(calls) != tc.want {
				t.Fatalf("commands = %q; want %q", calls, tc.want)
			}
		})
	}
}

func TestConfigureFirewallStopsOnFailure(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	t.Setenv("UFW_LOG", log)
	stub := `#!/bin/sh
printf '%s\n' "$*" >> "$UFW_LOG"
if [ "$2" = 80/tcp ]; then echo firewall-output; echo firewall-error >&2; exit 9; fi
`
	if err := os.WriteFile(filepath.Join(dir, "ufw"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := config.Default()
	cfg.MasqueradeEnabled = true
	err := ConfigureFirewall(cfg)
	var failure *CommandError
	if !errors.As(err, &failure) || failure.ExitCode != 9 || failure.Stdout != "firewall-output\n" || failure.Stderr != "firewall-error\n" {
		t.Fatalf("lost failure details: %v", err)
	}
	calls, readErr := os.ReadFile(log)
	if readErr != nil || strings.Contains(string(calls), "443/tcp") {
		t.Fatalf("continued after failure: %s (%v)", calls, readErr)
	}
}

func TestMissingUFWRequiresManualConfiguration(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cfg := config.Default()
	cfg.MasqueradeEnabled = true
	err := ConfigureFirewall(cfg)
	var skipped *SkippedError
	if !errors.As(err, &skipped) {
		t.Fatalf("expected skipped step, got %v", err)
	}
	for _, want := range []string{"ufw unavailable", "manual firewall configuration required", "443/udp", "80/tcp", "443/tcp"} {
		if !strings.Contains(skipped.Reason, want) {
			t.Errorf("missing %q: %v", want, err)
		}
	}
}
