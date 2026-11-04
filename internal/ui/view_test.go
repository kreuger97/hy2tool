package ui

import (
	"bytes"
	"errors"
	"github.com/kreuger97/hy2tool/internal/install"

	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/kreuger97/hy2tool/internal/config"
)

func TestWelcomeView(t *testing.T) {
	lipgloss.SetHasDarkBackground(true)

	m := model{
		state:   stateWelcome,
		cfg:     config.Default(),
		width:   80,
		height:  24,
		ready:   true,
		taskIdx: -1,
	}

	view := m.View()
	if view == "" {
		t.Fatal("welcome view is empty")
	}
	if !strings.Contains(view, "Hysteria 2") {
		t.Error("welcome view missing title")
	}
	if !strings.Contains(view, "Enter") {
		t.Error("welcome view missing prompt")
	}
	t.Logf("welcome view:\n%s", view)
}

func TestProcessingView(t *testing.T) {
	lipgloss.SetHasDarkBackground(true)

	m := model{
		state:   stateProcessing,
		cfg:     config.Default(),
		width:   80,
		height:  24,
		ready:   true,
		taskIdx: 0,
		tasks: []task{
			{name: "Install Hysteria 2", status: taskRunning},
			{name: "Generate SSL Certificate", status: taskPending},
			{name: "Write Server Config", status: taskPending},
		},
	}

	view := m.View()
	if view == "" {
		t.Fatal("processing view is empty")
	}
	t.Logf("processing view:\n%s", view)
}

func TestSummaryView(t *testing.T) {
	lipgloss.SetHasDarkBackground(true)

	m := model{
		state:    stateSummary,
		cfg:      config.Config{Port: "443", Password: "test123"},
		width:    80,
		height:   24,
		ready:    true,
		serverIP: "1.2.3.4",
	}

	view := m.View()
	if view == "" {
		t.Fatal("summary view is empty")
	}
	if !strings.Contains(view, "1.2.3.4") {
		t.Error("summary view missing server IP")
	}
	if !strings.Contains(view, "test123") {
		t.Error("summary view missing password")
	}
	if !strings.Contains(view, "Installation Complete") {
		t.Error("summary view missing title")
	}
	t.Logf("summary view:\n%s", view)
}

func TestFailedInstallationReport(t *testing.T) {
	failure := &install.CommandError{Command: "systemctl start hysteria-server", ExitCode: 1, Stdout: "output", Stderr: "failure", Cause: errors.New("exit status 1"), Diagnostics: "\nActive: failed\njournal-details"}
	m := initialModel()
	m.state, m.ready, m.taskIdx = stateProcessing, true, 1
	m.tasks = []task{{name: "certificate", status: taskDone}, {name: "service", status: taskRunning}, {name: "firewall", status: taskPending}}
	updated, cmd := m.Update(taskErrMsg{failure})
	m = updated.(model)
	if cmd != nil || m.state != stateSummary || m.tasks[2].status != taskSkipped {
		t.Fatalf("unexpected failure transition: %+v", m)
	}
	var out bytes.Buffer
	if err := printFinalReport(&out, m, nil); err == nil {
		t.Fatal("failure should propagate to CLI")
	}
	for _, report := range []string{m.View(), out.String()} {
		if strings.Contains(report, "Installation Complete") {
			t.Fatal("false success title")
		}
		for _, want := range []string{"Installation Failed", "[done] certificate", "[failed] service", "[skipped] firewall", "Exit code: 1", "stdout:", "output", "stderr:", "failure", "Active: failed", "journal-details"} {
			if !strings.Contains(report, want) {
				t.Errorf("missing %q in %s", want, report)
			}
		}
	}
}

func TestFinalSuccessAndInterruptedReports(t *testing.T) {
	m := initialModel()
	m.tasks = []task{{name: "install", status: taskSkipped, reason: "already installed"}, {name: "config", status: taskDone}}
	m.serverIP = "1.2.3.4"
	var out bytes.Buffer
	if err := printFinalReport(&out, m, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Installation Complete", "[skipped] install (already installed)", "[done] config", "Connection Link:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	m.tasks[1].status = taskRunning
	if strings.Contains(m.report(), "Installation Complete") || !strings.Contains(m.report(), "Installation Interrupted") {
		t.Fatal("interrupted work reported as success")
	}
	out.Reset()
	if err := printFinalReport(&out, m, errors.New("TUI failed")); err == nil || !strings.Contains(out.String(), "TUI failed") {
		t.Fatal("TUI error lost")
	}
}

func TestSkipAlreadyInstalledTask(t *testing.T) {
	m := initialModel()
	m.tasks = []task{{name: "Install Hysteria 2", status: taskSkipped}, {name: "Generate SSL Certificate"}}
	cmd := m.startNextTask()
	if cmd == nil || m.taskIdx != 1 || m.tasks[0].status != taskSkipped || m.tasks[1].status != taskRunning {
		t.Fatal("skipped stage was executed")
	}
}

func TestServiceSetupPrecedesStart(t *testing.T) {
	m := initialModel()
	m.buildTasks()
	setup, start := -1, -1
	for i, task := range m.tasks {
		switch task.name {
		case "Ensure systemd Service":
			setup = i
		case "Start & Enable Service":
			start = i
		}
	}
	if setup < 0 || start != setup+1 {
		t.Fatalf("service setup must precede start: %+v", m.tasks)
	}
	m.taskIdx = setup - 1
	if cmd := m.startNextTask(); cmd == nil || m.tasks[setup].status != taskRunning {
		t.Fatal("service setup command missing")
	}
}
