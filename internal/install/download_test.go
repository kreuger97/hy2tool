package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallHysteriaDownloadAndExecution(t *testing.T) {
	for _, tc := range []struct {
		force                bool
		name                 string
		curlExit, scriptExit int
		empty, missingCurl   bool
		wantCommand          string
		wantExit             int
		wantRun, wantErr     bool
	}{
		{name: "success", wantRun: true},
		{name: "forced reinstall", force: true, wantRun: true},
		{name: "HTTP failure", curlExit: 22, wantCommand: "curl", wantExit: 22, wantErr: true},
		{name: "partial download", curlExit: 18, wantCommand: "curl", wantExit: 18, wantErr: true},
		{name: "timeout", curlExit: 28, wantCommand: "curl", wantExit: 28, wantErr: true},
		{name: "script failure", scriptExit: 7, wantCommand: "bash", wantExit: 7, wantRun: true, wantErr: true},
		{name: "empty download", empty: true, wantErr: true},
		{name: "curl missing", missingCurl: true, wantCommand: "curl", wantExit: -1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, tempDir := t.TempDir(), t.TempDir()
			t.Setenv("TMPDIR", tempDir)
			t.Setenv("SCRIPT_MARKER", filepath.Join(dir, "script-ran"))
			t.Setenv("DOWNLOADED_PATH", filepath.Join(dir, "downloaded-path"))
			t.Setenv("SCRIPT_ARGUMENTS", filepath.Join(dir, "script-arguments"))
			t.Setenv("PAYLOAD", filepath.Join(dir, "payload"))
			payload := fmt.Sprintf("#!/bin/bash\nprintf ran > \"$SCRIPT_MARKER\"\necho script-output\necho script-error >&2\nexit %d\n", tc.scriptExit)
			payload = "printf '%s' \"$*\" > \"$SCRIPT_ARGUMENTS\"\n" + payload
			if tc.empty {
				payload = ""
			}
			if err := os.WriteFile(filepath.Join(dir, "payload"), []byte(payload), 0600); err != nil {
				t.Fatal(err)
			}
			if !tc.missingCurl {
				stub := fmt.Sprintf(`#!/bin/sh
while [ "$#" -gt 0 ]; do
 if [ "$1" = -o ]; then shift; output=$1; fi
 shift
done
printf '%%s' "$output" > "$DOWNLOADED_PATH"
cp "$PAYLOAD" "$output"
echo download-output
echo download-error >&2
exit %d
`, tc.curlExit)
				if err := os.WriteFile(filepath.Join(dir, "curl"), []byte(stub), 0755); err != nil {
					t.Fatal(err)
				}
			}
			path := dir + string(os.PathListSeparator) + os.Getenv("PATH")
			if tc.missingCurl {
				path = dir
			}
			t.Setenv("PATH", path)
			var err error
			if tc.force {
				err = ReinstallHysteria()
			} else {
				err = InstallHysteria()
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr = %t", err, tc.wantErr)
			}
			if tc.wantCommand != "" {
				var failure *CommandError
				if !errors.As(err, &failure) || failure.ExitCode != tc.wantExit || !strings.HasPrefix(failure.Command, fmt.Sprintf("%q", tc.wantCommand)) {
					t.Fatalf("wrong command error: %v", err)
				}
				if !tc.missingCurl {
					prefix := "download"
					if tc.wantRun {
						prefix = "script"
					}
					if failure.Stdout != prefix+"-output\n" || failure.Stderr != prefix+"-error\n" {
						t.Fatalf("streams lost: %+v", failure)
					}
				}
			}
			if tc.wantRun {
				args, readErr := os.ReadFile(filepath.Join(dir, "script-arguments"))
				want := ""
				if tc.force {
					want = "--force"
				}
				if readErr != nil || string(args) != want {
					t.Fatalf("installer args: %q %v", args, readErr)
				}
			}
			_, markerErr := os.Stat(filepath.Join(dir, "script-ran"))
			if tc.wantRun && markerErr != nil {
				t.Fatalf("script did not run: %v", markerErr)
			}
			if !tc.wantRun && !os.IsNotExist(markerErr) {
				t.Fatal("executed failed or partial download")
			}
			remaining, readErr := os.ReadDir(tempDir)
			if readErr != nil || len(remaining) != 0 {
				t.Fatalf("installer temporary files remain: %v, %v", remaining, readErr)
			}
			if !tc.missingCurl {
				path, readErr := os.ReadFile(filepath.Join(dir, "downloaded-path"))
				if readErr != nil {
					t.Fatal(readErr)
				}
				if _, err := os.Stat(string(path)); !os.IsNotExist(err) {
					t.Fatalf("downloaded script was not removed: %s", path)
				}
			}
		})
	}
}
