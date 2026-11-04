package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckDependencies(t *testing.T) {
	required := []string{"bash", "curl", "openssl", "chmod", "systemctl", "journalctl"}
	for _, tc := range []struct {
		name    string
		missing map[string]bool
		want    string
	}{
		{name: "all required present without ufw"},
		{name: "curl missing", missing: map[string]bool{"curl": true}, want: "curl"},
		{name: "multiple missing", missing: map[string]bool{"curl": true, "openssl": true, "systemctl": true}, want: "curl, openssl, systemctl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, command := range required {
				if !tc.missing[command] {
					if err := os.WriteFile(filepath.Join(dir, command), []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
						t.Fatal(err)
					}
				}
			}
			t.Setenv("PATH", dir)
			err := CheckDependencies()
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "missing required dependencies: "+tc.want) {
				t.Fatalf("missing dependency list: %v", err)
			}
		})
	}
}
