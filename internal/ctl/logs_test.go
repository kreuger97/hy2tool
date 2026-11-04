package ctl

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLogsArgs(t *testing.T) {
	base := []string{"--unit", "hysteria-server.service", "--no-pager", "--lines"}
	for _, tc := range []struct {
		name       string
		args, tail []string
	}{
		{"default", nil, []string{"40"}},
		{"last records", []string{"-n", "100"}, []string{"100"}},
		{"follow", []string{"-f"}, []string{"40", "--follow"}},
		{"follow history", []string{"-n", "100", "-f"}, []string{"100", "--follow"}},
		{"new only", []string{"-f", "-n", "0"}, []string{"0", "--follow"}},
		{"long flags", []string{"--lines=12", "--follow"}, []string{"12", "--follow"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := logsArgs(tc.args)
			want := append(append([]string{}, base...), tc.tail...)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %v, %v; want %v", got, err, want)
			}
		})
	}
	for _, args := range [][]string{{"-n", "-1"}, {"-n", "abc"}, {"-n"}, {"--unknown"}, {"another.service"}, {"-n", "99999999999999999999999999"}} {
		if _, err := logsArgs(args); err == nil {
			t.Fatalf("accepted invalid args %v", args)
		}
	}
	for _, help := range []string{"-h", "--help"} {
		if _, err := logsArgs([]string{help}); !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("help: %v", err)
		}
	}
}

// A subprocess exercises the real terminal stream wiring without replacing
// process-global stdout/stderr in the parent test runner.
func TestLogsHelper(t *testing.T) {
	if os.Getenv("HY2_LOGS_HELPER") != "1" {
		return
	}
	if err := CmdLogs(strings.Fields(os.Getenv("HY2_LOGS_ARGS"))); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func logsHelper(t *testing.T, args, script string) *exec.Cmd {
	t.Helper()
	dir := t.TempDir()
	if script != "" {
		if err := os.WriteFile(filepath.Join(dir, "journalctl"), []byte("#!/bin/sh\n"+script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLogsHelper$")
	cmd.Env = append(os.Environ(), "HY2_LOGS_HELPER=1", "HY2_LOGS_ARGS="+args, "PATH="+dir)
	return cmd
}

func TestCmdLogs(t *testing.T) {
	for _, tc := range []struct {
		name, args, script, stdout, stderr string
		wantErr                            bool
	}{
		{name: "journal output", args: "-n 2", script: "printf '%s\\n' \"$*\"\necho record-one\necho record-two\necho journal-warning >&2\n", stdout: "--unit hysteria-server.service --no-pager --lines 2\nrecord-one\nrecord-two\n", stderr: "journal-warning"},
		{name: "journal failure", script: "echo permission-denied >&2\nexit 7\n", stderr: "journalctl: exit status 7", wantErr: true},
		{name: "missing journalctl", stderr: "journalctl", wantErr: true},
		{name: "help", args: "--help", stdout: LogsUsage},
		{name: "invalid arguments", args: "-n -2", script: "echo should-not-run\n", stderr: "non-negative", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := logsHelper(t, tc.args, tc.script)
			var out, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &stderr
			err := cmd.Run()
			if (err != nil) != tc.wantErr {
				t.Fatalf("error %v; stderr: %s", err, stderr.String())
			}
			if out.String() != tc.stdout {
				t.Fatalf("stdout %q, want %q", out.String(), tc.stdout)
			}
			if !strings.Contains(stderr.String(), tc.stderr) {
				t.Fatalf("stderr %q, want %q", stderr.String(), tc.stderr)
			}
		})
	}
}

func TestCmdLogsFollowStreamsImmediately(t *testing.T) {
	cmd := logsHelper(t, "-n 0 -f", "echo \"$*\"\necho first\nread token\necho \"second-$token\"\n")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	reader := bufio.NewReader(stdout)
	first, err := reader.ReadString('\n')
	if err != nil || first != "--unit hysteria-server.service --no-pager --lines 0 --follow\n" {
		t.Fatalf("args %q: %v", first, err)
	}
	first, err = reader.ReadString('\n')
	if err != nil || first != "first\n" {
		t.Fatalf("live output %q: %v", first, err)
	}
	// journalctl is still running and waiting for input; buffered output would
	// prevent the first read from completing before this input is sent.
	if _, err := fmt.Fprintln(stdin, "continue"); err != nil {
		t.Fatal(err)
	}
	second, err := reader.ReadString('\n')
	if err != nil || second != "second-continue\n" {
		t.Fatalf("stream %q: %v", second, err)
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("%v: %s", err, stderr.String())
	}
}
