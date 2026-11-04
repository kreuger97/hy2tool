package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateURL(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "386", "arm", "s390x", "mipsle", "riscv64"} {
		url, err := updateURL("linux", arch)
		if err != nil || url != "https://download.hysteria.network/app/latest/hysteria-linux-"+arch {
			t.Fatalf("%s: %s %v", arch, url, err)
		}
	}
	for _, platform := range [][2]string{{"darwin", "arm64"}, {"linux", "unknown"}} {
		if _, err := updateURL(platform[0], platform[1]); err == nil {
			t.Fatalf("accepted %v", platform)
		}
	}
}

func TestUpdatePreservesConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, state, download, restart, version string
		wantErr, wantOld                        bool
		restarts                                int
	}{
		{name: "running", state: "active", restarts: 1},
		{name: "stopped", state: "inactive"},
		{name: "failed", state: "failed"},
		{name: "download failure", state: "active", download: "fail", wantErr: true, wantOld: true},
		{name: "invalid download", state: "active", version: "bad", wantErr: true, wantOld: true},
		{name: "nonexecutable download", state: "active", version: "broken", wantErr: true, wantOld: true},
		{name: "restart rollback", state: "active", restart: "new-fails", wantErr: true, wantOld: true, restarts: 2},
		{name: "rollback restart failure", state: "active", restart: "always-fails", wantErr: true, wantOld: true, restarts: 2},
		{name: "activity check rollback", state: "active", restart: "inactive-new", wantErr: true, wantOld: true, restarts: 2},
		{name: "transitioning service", state: "activating", wantErr: true, wantOld: true},
		{name: "systemd failure", state: "unavailable", wantErr: true, wantOld: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "hysteria")
			old := "#!/bin/sh\necho 'Version: v2.0.0-old'\n"
			newBinary := "#!/bin/sh\necho 'Version: v2.99.0-new'\n"
			if tc.version == "bad" {
				newBinary = "#!/bin/sh\necho not-hysteria\n"
			}
			if tc.version == "broken" {
				newBinary = "#!/bin/sh\necho broken >&2\nexit 8\n"
			}
			write := func(name, data string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), mode); err != nil {
					t.Fatal(err)
				}
			}
			write("hysteria", old, 0755)
			write("payload", newBinary, 0600)
			protected := map[string]string{"config.yaml": "# custom\nauth:\n  password: keep-me\n", "cert.pem": "existing certificate", "key.pem": "private key", "hysteria-server.service": "custom unit"}
			before := make(map[string]os.FileInfo)
			for name, content := range protected {
				write(name, content, 0600)
				info, err := os.Stat(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				before[name] = info
			}
			write("curl", `#!/bin/sh
printf 'curl\n' >> "$UPDATE_LOG"
if [ "$DOWNLOAD_MODE" = fail ]; then echo download-error >&2; exit 22; fi
while [ "$#" -gt 0 ]; do
 if [ "$1" = --output ]; then shift; output=$1; fi
 shift
done
cp "$UPDATE_PAYLOAD" "$output"
`, 0755)
			write("systemctl", `#!/bin/sh
printf '%s\n' "$*" >> "$UPDATE_LOG"
case "$1" in
 show)
  if [ "$SERVICE_STATE" = unavailable ]; then echo systemd-error >&2; exit 1; fi
  echo "$SERVICE_STATE";;
 restart)
  version=$("$UPDATE_BINARY" version)
  if [ "$RESTART_MODE" = always-fails ]; then echo restart-error >&2; exit 1; fi
  case "$version" in *new*) if [ "$RESTART_MODE" = new-fails ]; then echo restart-error >&2; exit 1; fi;; esac;;
 is-active)
  version=$("$UPDATE_BINARY" version)
  case "$version" in *new*) if [ "$RESTART_MODE" = inactive-new ]; then exit 3; fi;; esac;;
esac
`, 0755)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("UPDATE_LOG", filepath.Join(dir, "calls"))
			t.Setenv("UPDATE_BINARY", binary)
			t.Setenv("UPDATE_PAYLOAD", filepath.Join(dir, "payload"))
			t.Setenv("SERVICE_STATE", tc.state)
			t.Setenv("DOWNLOAD_MODE", tc.download)
			t.Setenv("RESTART_MODE", tc.restart)
			// Exercise preservation of a symlink used to locate the executable.
			link := filepath.Join(dir, "hysteria-link")
			if err := os.Symlink(binary, link); err != nil {
				t.Fatal(err)
			}
			err := updateHysteria(link, "https://example.invalid/hysteria")
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %t", err, tc.wantErr)
			}
			data, err := os.ReadFile(binary)
			if err != nil {
				t.Fatal(err)
			}
			want := newBinary
			if tc.wantOld {
				want = old
			}
			if string(data) != want {
				t.Fatalf("wrong installed binary: %s", data)
			}
			if target, err := os.Readlink(link); err != nil || target != binary {
				t.Fatal("binary symlink changed")
			}
			for name, content := range protected {
				path := filepath.Join(dir, name)
				data, err := os.ReadFile(path)
				if err != nil || string(data) != content {
					t.Fatalf("changed %s", name)
				}
				info, err := os.Stat(path)
				if err != nil || !os.SameFile(before[name], info) || info.Mode() != before[name].Mode() || !info.ModTime().Equal(before[name].ModTime()) {
					t.Fatalf("metadata changed for %s", name)
				}
			}
			calls, err := os.ReadFile(filepath.Join(dir, "calls"))
			if err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(string(calls), "restart hysteria-server.service"); n != tc.restarts {
				t.Fatalf("restart count %d, want %d; calls: %s", n, tc.restarts, calls)
			}
			for _, forbidden := range []string{"enable", "daemon-reload", "start hysteria-server.service\nstart"} {
				if strings.Contains(string(calls), forbidden) {
					t.Fatalf("unexpected command: %s", calls)
				}
			}
			leftovers, _ := filepath.Glob(filepath.Join(dir, ".hysteria-*"))
			if len(leftovers) != 0 {
				t.Fatalf("temporary files left: %v", leftovers)
			}
		})
	}
}
