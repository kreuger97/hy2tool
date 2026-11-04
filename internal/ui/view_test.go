package ui

import (
	"bytes"
	"errors"
	"github.com/kreuger97/hy2tool/internal/install"

	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

func TestPreflightStopsBeforeInstallation(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	m := initialModel()
	m.buildTasks()
	m.state = stateProcessing
	m.ready = true
	if m.tasks[0].name != "Check Dependencies" {
		t.Fatal("dependency check must be first")
	}
	command := m.startNextTask()
	msg := command()
	if _, ok := msg.(taskErrMsg); !ok {
		t.Fatalf("missing commands should fail preflight: %T", msg)
	}
	updated, next := m.Update(msg)
	m = updated.(model)
	if next != nil || m.state != stateSummary || m.tasks[0].status != taskError {
		t.Fatal("preflight did not stop installation")
	}
	for _, stage := range m.tasks[1:] {
		if stage.status != taskSkipped {
			t.Fatalf("stage ran after preflight failure: %+v", stage)
		}
	}
	if !strings.Contains(m.report(), "missing required dependencies") || strings.Contains(m.View(), "Installation Complete") {
		t.Fatal("wrong dependency failure report")
	}
}

func TestMissingFirewallFinishesWithManualSetupReminder(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	m := initialModel()
	m.cfg.MasqueradeEnabled = true
	m.state = stateProcessing
	m.ready = true
	m.tasks = []task{{name: "Start & Enable Service", status: taskDone}, {name: "Configure Firewall"}}
	m.taskIdx = 0
	msg := m.startNextTask()()
	if _, ok := msg.(taskSkippedMsg); !ok {
		t.Fatalf("missing ufw should skip: %T", msg)
	}
	updated, next := m.Update(msg)
	m = updated.(model)
	if next != nil || m.state != stateSummary || m.errMsg != "" || m.tasks[1].status != taskSkipped {
		t.Fatal("optional firewall blocked completion")
	}
	var out bytes.Buffer
	if err := printFinalReport(&out, m, nil); err != nil {
		t.Fatal(err)
	}
	for _, report := range []string{m.report(), out.String()} {
		for _, want := range []string{"[skipped] Configure Firewall", "manual firewall configuration required", "443/udp", "80/tcp", "443/tcp"} {
			if !strings.Contains(report, want) {
				t.Errorf("missing %q in report: %s", want, report)
			}
		}
	}
	if !strings.Contains(m.View(), "manual") || !strings.Contains(m.View(), "ufw") {
		t.Fatal("TUI omitted manual setup reminder")
	}
}

func TestReinstallRequiresExplicitApproval(t *testing.T) {
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	m := initialModel()
	m.ready = true
	checked, checkCmd := m.Update(enter)
	m = checked.(model)
	if m.state != stateInspectInstallation || checkCmd == nil || len(m.tasks) != 0 {
		t.Fatal("installation started before detection")
	}
	detected, _ := m.Update(installationDetectedMsg{found: true})
	m = detected.(model)
	if m.state != stateConfirmReinstall || m.reinstall || len(m.tasks) != 0 {
		t.Fatal("existing install bypassed approval")
	}
	if !strings.Contains(m.View(), "Confirm Full Reinstallation") || !strings.Contains(m.View(), "replaced") {
		t.Fatal("missing destructive reinstall warning")
	}
	for _, answer := range []string{"", "n", "x", "y"} {
		t.Run("answer="+answer, func(t *testing.T) {
			candidate := m
			candidate.yesNoInp.SetValue(answer)
			updated, cmd := candidate.Update(enter)
			candidate = updated.(model)
			if answer != "y" {
				if candidate.reinstall || candidate.state != stateExit || len(candidate.tasks) != 0 || cmd == nil {
					t.Fatal("reinstall continued without approval")
				}
				if _, ok := cmd().(tea.QuitMsg); !ok {
					t.Fatal("decline should exit without operations")
				}
			} else {
				if !candidate.reinstall || candidate.state != stateAskPort || len(candidate.tasks) != 0 {
					t.Fatal("approval did not start configuration")
				}
				candidate.buildTasks()
				want := []string{"Check Dependencies", "Stop Previous Service", "Reinstall Hysteria 2", "Generate SSL Certificate", "Write Server Config", "Recreate systemd Service", "Restart & Enable Service", "Configure Firewall"}
				if len(candidate.tasks) != len(want) {
					t.Fatalf("wrong tasks: %+v", candidate.tasks)
				}
				for i, name := range want {
					if candidate.tasks[i].name != name || candidate.tasks[i].status != taskPending {
						t.Fatalf("reinstall stage %d: %+v", i, candidate.tasks[i])
					}
				}
			}
		})
	}
}

func TestFreshInstallationDoesNotRequireReinstallApproval(t *testing.T) {
	m := initialModel()
	m.state = stateInspectInstallation
	updated, _ := m.Update(installationDetectedMsg{found: false})
	m = updated.(model)
	if m.state != stateAskPort || m.reinstall {
		t.Fatal("fresh install incorrectly requires reinstall")
	}
	m.buildTasks()
	if m.tasks[1].name != "Install Hysteria 2" || m.tasks[1].status != taskPending {
		t.Fatal("fresh binary install skipped")
	}
}
