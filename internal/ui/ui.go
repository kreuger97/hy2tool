package ui

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/kreuger97/hy2tool/internal/config"
	"github.com/kreuger97/hy2tool/internal/install"
)

func generatePassword(length int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	password := make([]byte, length)
	for i := range password {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		password[i] = chars[n.Int64()]
	}
	return string(password)
}

type state int

const (
	stateWelcome state = iota
	stateInspectInstallation
	stateConfirmReinstall
	stateAskPort
	stateAskPassword
	stateAskMasquerade
	stateAskMasqueradeURL
	stateAskMasqueradeHTTPPort
	stateAskMasqueradeHTTPSPort
	stateAskMasqueradeForceHTTPS
	stateAskBandwidthUp
	stateAskBandwidthDown
	stateProcessing
	stateSummary
	stateExit
)

type taskStatus int

const (
	taskPending taskStatus = iota
	taskRunning
	taskDone
	taskError
	taskSkipped
)

type task struct {
	name   string
	status taskStatus
	err    error
	reason string
}

type model struct {
	reinstall    bool
	state        state
	cfg          config.Config
	tasks        []task
	taskIdx      int
	spinner      spinner.Model
	portInp      textinput.Model
	passInp      textinput.Model
	urlInp       textinput.Model
	httpPortInp  textinput.Model
	httpsPortInp textinput.Model
	bwUpInp      textinput.Model
	bwDownInp    textinput.Model
	yesNoInp     textinput.Model
	width        int
	height       int
	errMsg       string
	ready        bool
	serverIP     string
}

func initialModel() model {
	s := spinner.New()
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("69"))
	s.Spinner = spinner.Dot

	pi := textinput.New()
	pi.Placeholder = "443"
	pi.CharLimit = 5
	pi.Width = 6

	pa := textinput.New()
	pa.Placeholder = "press Enter for random password"
	pa.CharLimit = 64
	pa.Width = 30
	pa.EchoMode = textinput.EchoPassword

	ui := textinput.New()
	ui.Placeholder = "https://www.bing.com"
	ui.CharLimit = 256
	ui.Width = 40

	hp := textinput.New()
	hp.Placeholder = ":80"
	hp.CharLimit = 7
	hp.Width = 10

	hsp := textinput.New()
	hsp.Placeholder = ":443"
	hsp.CharLimit = 7
	hsp.Width = 10

	bu := textinput.New()
	bu.Placeholder = "30 mbps"
	bu.CharLimit = 16
	bu.Width = 16

	bd := textinput.New()
	bd.Placeholder = "80 mbps"
	bd.CharLimit = 16
	bd.Width = 16

	yn := textinput.New()
	yn.Placeholder = "y"
	yn.CharLimit = 1
	yn.Width = 3

	return model{
		state:        stateWelcome,
		cfg:          config.Default(),
		spinner:      s,
		portInp:      pi,
		passInp:      pa,
		urlInp:       ui,
		httpPortInp:  hp,
		httpsPortInp: hsp,
		bwUpInp:      bu,
		bwDownInp:    bd,
		yesNoInp:     yn,
		taskIdx:      -1,
	}
}

type installationDetectedMsg struct{ found bool }
type taskDoneMsg struct{}
type taskSkippedMsg struct{ reason string }
type taskErrMsg struct{ err error }

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spinner.Tick)
}

func (m *model) buildTasks() {
	m.taskIdx = -1
	m.tasks = []task{{name: "Check Dependencies"}}
	if m.reinstall {
		m.tasks = append(m.tasks, task{name: "Stop Previous Service"}, task{name: "Reinstall Hysteria 2"})
	} else {
		m.tasks = append(m.tasks, task{name: "Install Hysteria 2"})
	}

	m.tasks = append(m.tasks,
		task{name: "Generate SSL Certificate"},
		task{name: "Write Server Config"},
		task{name: "Ensure systemd Service"},
		task{name: "Start & Enable Service"},
		task{name: "Configure Firewall"},
	)
	if m.reinstall {
		for i := range m.tasks {
			switch m.tasks[i].name {
			case "Ensure systemd Service":
				m.tasks[i].name = "Recreate systemd Service"
			case "Start & Enable Service":
				m.tasks[i].name = "Restart & Enable Service"
			}
		}
	}

}

func (m model) currentTask() *task {
	if m.taskIdx >= 0 && m.taskIdx < len(m.tasks) {
		return &m.tasks[m.taskIdx]
	}
	return nil
}

func (m model) allTasksDone() bool {
	for _, t := range m.tasks {
		if t.status != taskDone && t.status != taskSkipped {
			return false
		}
	}
	return true
}

func (m *model) startNextTask() tea.Cmd {
	m.taskIdx++
	for m.taskIdx < len(m.tasks) && m.tasks[m.taskIdx].status == taskSkipped {
		m.taskIdx++
	}
	if m.taskIdx >= len(m.tasks) {
		return nil
	}
	t := &m.tasks[m.taskIdx]
	t.status = taskRunning

	var cmd tea.Cmd
	switch t.name {
	case "Check Dependencies":
		cmd = func() tea.Msg {
			if err := install.CheckDependencies(); err != nil {
				return taskErrMsg{err}
			}
			return taskDoneMsg{}
		}
	case "Stop Previous Service":
		cmd = func() tea.Msg {
			if err := install.StopForReinstall(); err != nil {
				var skipped *install.SkippedError
				if errors.As(err, &skipped) {
					return taskSkippedMsg{reason: skipped.Reason}
				}
				return taskErrMsg{err}
			}
			return taskDoneMsg{}
		}
	case "Reinstall Hysteria 2":
		cmd = func() tea.Msg {
			if err := install.ReinstallHysteria(); err != nil {
				return taskErrMsg{err}
			}
			return taskDoneMsg{}
		}
	case "Recreate systemd Service":
		cmd = func() tea.Msg {
			if err := install.RecreateService(); err != nil {
				return taskErrMsg{err}
			}
			return taskDoneMsg{}
		}
	case "Restart & Enable Service":
		cmd = func() tea.Msg {
			if err := install.RestartService(); err != nil {
				return taskErrMsg{err}
			}
			return taskDoneMsg{}
		}
	case "Install Hysteria 2":
		cmd = func() tea.Msg {
			if err := install.InstallHysteria(); err != nil {
				return taskErrMsg{err}
			}
			return taskDoneMsg{}
		}
	case "Generate SSL Certificate":
		cmd = func() tea.Msg {
			if err := install.GenerateCert(); err != nil {
				return taskErrMsg{err}
			}
			return taskDoneMsg{}
		}
	case "Write Server Config":
		cmd = func() tea.Msg {
			if err := install.WriteConfig(m.cfg); err != nil {
				return taskErrMsg{err}
			}
			return taskDoneMsg{}
		}
	case "Ensure systemd Service":
		cmd = func() tea.Msg {
			if err := install.EnsureService(); err != nil {
				return taskErrMsg{err}
			}
			return taskDoneMsg{}
		}
	case "Start & Enable Service":
		cmd = func() tea.Msg {
			if err := install.StartService(); err != nil {
				return taskErrMsg{err}
			}
			return taskDoneMsg{}
		}
	case "Configure Firewall":
		cmd = func() tea.Msg {
			if err := install.ConfigureFirewall(m.cfg); err != nil {
				var skipped *install.SkippedError
				if errors.As(err, &skipped) {
					return taskSkippedMsg{reason: skipped.Reason}
				}
				return taskErrMsg{err}
			}
			return taskDoneMsg{}
		}
	}
	return cmd
}

func getOutboundIP() string {
	providers := []string{
		"curl -fsSL --connect-timeout 3 https://ipinfo.io/ip",
		"curl -fsSL --connect-timeout 3 https://api.ipify.org",
	}
	for _, p := range providers {
		out, err := exec.Command("bash", "-c", p).Output()
		if err == nil {
			ip := strings.TrimSpace(string(out))
			if ip != "" {
				return ip
			}
		}
	}
	out, err := exec.Command("hostname", "-I").Output()
	if err == nil {
		parts := strings.Fields(string(out))
		if len(parts) > 0 {
			return parts[0]
		}
	}
	return ""
}

func (m model) connectionURI() string {
	host := m.serverIP
	if host == "" {
		host = "<server-ip>"
	}
	sni := config.MasqueradeSNI(m.cfg.MasqueradeURL)
	u := url.URL{
		Scheme: "hysteria2",
		User:   url.User(m.cfg.Password),
		Host:   fmt.Sprintf("%s:%s", host, m.cfg.Port),
		RawQuery: url.Values{
			"insecure": {"1"},
			"alpn":     {"h3"},
			"sni":      {sni},
		}.Encode(),
	}
	return u.String()
}

func (m model) qrCode() string {
	qr, err := qrcode.New(m.connectionURI(), qrcode.Medium)
	if err != nil {
		return ""
	}
	return qr.ToString(true)
}

func yesNo(val string) bool {
	v := strings.TrimSpace(strings.ToLower(val))
	return v == "y" || v == "yes"
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case installationDetectedMsg:
		if m.state != stateInspectInstallation {
			return m, nil
		}
		if msg.found {
			m.state = stateConfirmReinstall
			m.yesNoInp.SetValue("")
			m.yesNoInp.Focus()
			return m, textinput.Blink
		}
		m.state = stateAskPort
		m.portInp.Focus()
		return m, textinput.Blink
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.state == stateWelcome || m.state == stateSummary || m.state == stateConfirmReinstall || m.state == stateInspectInstallation {
				return m, tea.Quit
			}
		}

		if msg.String() == "q" && m.state == stateProcessing {
			return m, tea.Quit
		}
		switch m.state {
		case stateWelcome:
			if msg.String() == "enter" {
				m.cfg = config.Default()
				m.reinstall = false
				m.state = stateInspectInstallation
				m.portInp.SetValue(m.cfg.Port)
				return m, func() tea.Msg { return installationDetectedMsg{found: install.PreviousInstallation()} }
			}

		case stateConfirmReinstall:
			switch msg.String() {
			case "enter":
				if !yesNo(m.yesNoInp.Value()) {
					m.state = stateExit
					return m, tea.Quit
				}
				m.reinstall = true
				m.state = stateAskPort
				m.portInp.Focus()
				return m, textinput.Blink
			case "esc":
				m.state = stateWelcome
				return m, nil
			}
			var cmd tea.Cmd
			m.yesNoInp, cmd = m.yesNoInp.Update(msg)
			return m, cmd
		case stateAskPort:
			switch msg.String() {
			case "enter":
				if m.portInp.Value() != "" {
					m.cfg.Port = m.portInp.Value()
				}
				m.state = stateAskPassword
				m.passInp.Focus()
				return m, textinput.Blink
			case "esc":
				m.state = stateWelcome
				return m, nil
			}
			var cmd tea.Cmd
			m.portInp, cmd = m.portInp.Update(msg)
			return m, cmd

		case stateAskPassword:
			switch msg.String() {
			case "enter":
				if m.passInp.Value() == "" {
					m.cfg.Password = generatePassword(16)
				} else {
					m.cfg.Password = m.passInp.Value()
				}
				m.state = stateAskMasquerade
				m.yesNoInp.SetValue("")
				m.yesNoInp.Focus()
				return m, textinput.Blink
			case "esc":
				m.state = stateAskPort
				m.portInp.Focus()
				return m, textinput.Blink
			}
			var cmd tea.Cmd
			m.passInp, cmd = m.passInp.Update(msg)
			return m, cmd

		case stateAskMasquerade:
			switch msg.String() {
			case "enter":
				val := m.yesNoInp.Value()
				if val == "" || yesNo(val) {
					m.cfg.MasqueradeEnabled = true
					m.state = stateAskMasqueradeURL
					m.urlInp.SetValue(m.cfg.MasqueradeURL)
					m.urlInp.Focus()
				} else {
					m.cfg.MasqueradeEnabled = false
					m.state = stateAskBandwidthUp
					m.bwUpInp.SetValue(m.cfg.BandwidthUp)
					m.bwUpInp.Focus()
				}
				return m, textinput.Blink
			case "esc":
				m.state = stateAskPassword
				m.passInp.Focus()
				return m, textinput.Blink
			}
			var cmd tea.Cmd
			m.yesNoInp, cmd = m.yesNoInp.Update(msg)
			return m, cmd

		case stateAskMasqueradeURL:
			switch msg.String() {
			case "enter":
				if m.urlInp.Value() != "" {
					m.cfg.MasqueradeURL = m.urlInp.Value()
				}
				m.state = stateAskMasqueradeHTTPPort
				m.httpPortInp.SetValue(m.cfg.MasqueradeHTTPPort)
				m.httpPortInp.Focus()
				return m, textinput.Blink
			case "esc":
				m.state = stateAskMasquerade
				m.yesNoInp.Focus()
				return m, textinput.Blink
			}
			var cmd tea.Cmd
			m.urlInp, cmd = m.urlInp.Update(msg)
			return m, cmd

		case stateAskMasqueradeHTTPPort:
			switch msg.String() {
			case "enter":
				if m.httpPortInp.Value() != "" {
					m.cfg.MasqueradeHTTPPort = m.httpPortInp.Value()
				}
				m.state = stateAskMasqueradeHTTPSPort
				m.httpsPortInp.SetValue(m.cfg.MasqueradeHTTPSPort)
				m.httpsPortInp.Focus()
				return m, textinput.Blink
			case "esc":
				m.state = stateAskMasqueradeURL
				m.urlInp.Focus()
				return m, textinput.Blink
			}
			var cmd tea.Cmd
			m.httpPortInp, cmd = m.httpPortInp.Update(msg)
			return m, cmd

		case stateAskMasqueradeHTTPSPort:
			switch msg.String() {
			case "enter":
				if m.httpsPortInp.Value() != "" {
					m.cfg.MasqueradeHTTPSPort = m.httpsPortInp.Value()
				}
				m.state = stateAskMasqueradeForceHTTPS
				m.yesNoInp.SetValue("")
				m.yesNoInp.Focus()
				return m, textinput.Blink
			case "esc":
				m.state = stateAskMasqueradeHTTPPort
				m.httpPortInp.Focus()
				return m, textinput.Blink
			}
			var cmd tea.Cmd
			m.httpsPortInp, cmd = m.httpsPortInp.Update(msg)
			return m, cmd

		case stateAskMasqueradeForceHTTPS:
			switch msg.String() {
			case "enter":
				val := m.yesNoInp.Value()
				if val == "" || yesNo(val) {
					m.cfg.MasqueradeForceHTTPS = true
				} else {
					m.cfg.MasqueradeForceHTTPS = false
				}
				m.state = stateAskBandwidthUp
				m.bwUpInp.SetValue(m.cfg.BandwidthUp)
				m.bwUpInp.Focus()
				return m, textinput.Blink
			case "esc":
				m.state = stateAskMasqueradeHTTPSPort
				m.httpsPortInp.Focus()
				return m, textinput.Blink
			}
			var cmd tea.Cmd
			m.yesNoInp, cmd = m.yesNoInp.Update(msg)
			return m, cmd

		case stateAskBandwidthUp:
			switch msg.String() {
			case "enter":
				if m.bwUpInp.Value() != "" {
					m.cfg.BandwidthUp = m.bwUpInp.Value()
				}
				m.state = stateAskBandwidthDown
				m.bwDownInp.SetValue(m.cfg.BandwidthDown)
				m.bwDownInp.Focus()
				return m, textinput.Blink
			case "esc":
				if m.cfg.MasqueradeEnabled {
					m.state = stateAskMasqueradeForceHTTPS
					m.yesNoInp.Focus()
				} else {
					m.state = stateAskMasquerade
					m.yesNoInp.Focus()
				}
				return m, textinput.Blink
			}
			var cmd tea.Cmd
			m.bwUpInp, cmd = m.bwUpInp.Update(msg)
			return m, cmd

		case stateAskBandwidthDown:
			switch msg.String() {
			case "enter":
				if m.bwDownInp.Value() != "" {
					m.cfg.BandwidthDown = m.bwDownInp.Value()
				}
				m.buildTasks()
				m.state = stateProcessing
				cmds := []tea.Cmd{m.startNextTask(), m.spinner.Tick}
				return m, tea.Batch(cmds...)
			case "esc":
				m.state = stateAskBandwidthUp
				m.bwUpInp.Focus()
				return m, textinput.Blink
			}
			var cmd tea.Cmd
			m.bwDownInp, cmd = m.bwDownInp.Update(msg)
			return m, cmd

		case stateSummary:
			if msg.String() == "enter" || msg.String() == "q" {
				return m, tea.Quit
			}
		}

	case taskDoneMsg, taskSkippedMsg:
		t := m.currentTask()
		if t != nil {
			if skipped, ok := msg.(taskSkippedMsg); ok {
				t.status, t.reason = taskSkipped, skipped.reason
			} else {
				t.status = taskDone
			}
		}
		if m.allTasksDone() {
			m.serverIP = getOutboundIP()
			m.state = stateSummary
			return m, nil
		}
		return m, m.startNextTask()

	case taskErrMsg:
		t := m.currentTask()
		if t != nil {
			t.status = taskError
			t.err = msg.err
		}
		for i := m.taskIdx + 1; i < len(m.tasks); i++ {
			m.tasks[i].status = taskSkipped
			m.tasks[i].reason = "previous stage failed"
		}
		m.errMsg = msg.err.Error()
		m.state = stateSummary
		return m, nil

	default:
		if m.state == stateProcessing {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}

func (m model) View() string {
	if !m.ready {
		return "\n  Loading..."
	}

	switch m.state {
	case stateInspectInstallation:
		return "\nChecking for an existing Hysteria installation..."
	case stateConfirmReinstall:
		input := m.yesNoInp
		input.Placeholder = "n"
		return m.askYesNoView("Confirm Full Reinstallation", "Existing Hysteria installation detected.\nThe server will be stopped. The binary, config,\ncertificates and main systemd unit will be replaced.\nExisting clients may need new connection settings.\nContinue with full reinstallation?", input, "y/N")
	case stateWelcome:
		return m.welcomeView()
	case stateAskPort:
		return m.askPortView()
	case stateAskPassword:
		return m.askPasswordView()
	case stateAskMasquerade:
		return m.askYesNoView("Enable Masquerade", "Use a reverse proxy to mask Hysteria traffic.\nThis makes the server look like a regular HTTPS website.", m.yesNoInp, "Y/n")
	case stateAskMasqueradeURL:
		return m.askInputView("Masquerade URL", "The website to proxy when accessed directly.", m.urlInp)
	case stateAskMasqueradeHTTPPort:
		return m.askInputView("Masquerade HTTP Port", "Port for plain HTTP traffic.", m.httpPortInp)
	case stateAskMasqueradeHTTPSPort:
		return m.askInputView("Masquerade HTTPS Port", "Port for HTTPS traffic.", m.httpsPortInp)
	case stateAskMasqueradeForceHTTPS:
		return m.askYesNoView("Force HTTPS", "Redirect all HTTP traffic to HTTPS.", m.yesNoInp, "Y/n")
	case stateAskBandwidthUp:
		return m.askInputView("Bandwidth Upload", "Maximum upload bandwidth per client.", m.bwUpInp)
	case stateAskBandwidthDown:
		return m.askInputView("Bandwidth Download", "Maximum download bandwidth per client.", m.bwDownInp)
	case stateProcessing:
		return m.processingView()
	case stateSummary:
		return m.summaryView()
	default:
		return ""
	}
}

func (m model) welcomeView() string {
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("69")).
		Render("Hysteria 2 Server Installer")

	desc := lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Render(
		"This tool will guide you through installing and configuring\n" +
			"Hysteria 2 on this server.",
	)

	items := []string{
		"  • Install Hysteria 2",
		"  • Generate self-signed SSL certificate",
		"  • Configure server settings",
		"  • Configure masquerade (optional)",
		"  • Configure bandwidth limits",
		"  • Start systemd service",
		"  • Configure firewall",
	}

	prompt := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("36")).
		Render("\n  Press Enter to start  •  q to quit")

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		desc,
		"",
		lipgloss.JoinVertical(lipgloss.Left, items...),
		prompt,
	)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2).
		Width(48).
		Render(content)

	return "\n" + centerBox(box, m.width)
}

func (m model) askPortView() string {
	return m.askInputView("Server Port", "Choose a UDP port for the Hysteria server.\n443 is recommended for DPI bypass.", m.portInp)
}

func (m model) askPasswordView() string {
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("69")).
		Render("Auth Password")

	desc := lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Render(
		"Set a password for client authentication.\nLeave empty and press Enter to generate a random password.",
	)

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		desc,
		"",
		"Password: "+m.passInp.View(),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color("242")).Render("Enter to confirm  •  Esc to go back"),
	)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2).
		Width(48).
		Render(content)

	return "\n" + centerBox(box, m.width)
}

func (m model) askInputView(title, desc string, input textinput.Model) string {
	t := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("69")).
		Render(title)

	d := lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Render(desc)

	content := lipgloss.JoinVertical(lipgloss.Left,
		t,
		"",
		d,
		"",
		input.Placeholder+": "+input.View(),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color("242")).Render("Enter to confirm  •  Esc to go back"),
	)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2).
		Width(48).
		Render(content)

	return "\n" + centerBox(box, m.width)
}

func (m model) askYesNoView(title, desc string, input textinput.Model, placeholder string) string {
	t := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("69")).
		Render(title)

	d := lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Render(desc)

	content := lipgloss.JoinVertical(lipgloss.Left,
		t,
		"",
		d,
		"",
		placeholder+": "+input.View(),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color("242")).Render("Enter to confirm  •  Esc to go back"),
	)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2).
		Width(48).
		Render(content)

	return "\n" + centerBox(box, m.width)
}

func (m model) processingView() string {
	var b strings.Builder

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("69")).
		Render("Installing & Configuring")

	b.WriteString(title)
	b.WriteString("\n\n")

	for i, t := range m.tasks {
		var line string
		switch t.status {
		case taskPending:
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("  ○ " + t.name)
		case taskRunning:
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("69")).Render("  " + m.spinner.View() + " " + t.name)
		case taskDone:
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("36")).Render("  ✓ " + t.name)
		case taskSkipped:
			line = "  − " + t.name + " (skipped: " + t.reason + ")"
		case taskError:
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("  ✗ " + t.name)
		}
		b.WriteString(line)
		if i < len(m.tasks)-1 {
			b.WriteString("\n")
		}
	}

	content := b.String()

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2).
		Width(48).
		Render(content)

	return "\n" + centerBox(box, m.width)
}

func (m model) summaryView() string {
	if m.errMsg != "" || !m.allTasksDone() {
		return m.report() + "\nPress Enter or q to quit"
	}
	var b strings.Builder

	serverIP := m.serverIP
	if serverIP == "" {
		serverIP = "<server-ip>"
	}

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("36")).
		Render("Installation Complete")

	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString(m.stageReport())
	b.WriteString("\n")

	infoStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	b.WriteString(infoStyle.Render("  Server:     " + serverIP))
	b.WriteString("\n")
	b.WriteString(infoStyle.Render("  Port:       " + m.cfg.Port))
	b.WriteString("\n")
	b.WriteString(infoStyle.Render("  Password:   " + m.cfg.Password))
	b.WriteString("\n")

	if m.cfg.MasqueradeEnabled {
		b.WriteString(infoStyle.Render("  Masquerade: ON (" + m.cfg.MasqueradeURL + ")"))
		b.WriteString("\n")
	} else {
		b.WriteString(infoStyle.Render("  Masquerade: OFF"))
		b.WriteString("\n")
	}

	b.WriteString(infoStyle.Render("  Bandwidth:  ↑" + m.cfg.BandwidthUp + " ↓" + m.cfg.BandwidthDown))
	b.WriteString("\n\n")

	clientLabel := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("228")).Render("Client Configuration")
	b.WriteString(clientLabel)
	b.WriteString("\n\n")

	b.WriteString(infoStyle.Render("  Protocol:       Hysteria 2"))
	b.WriteString("\n")
	b.WriteString(infoStyle.Render("  Server:         " + serverIP))
	b.WriteString("\n")
	b.WriteString(infoStyle.Render("  Port:           " + m.cfg.Port))
	b.WriteString("\n")
	b.WriteString(infoStyle.Render("  Password:       " + m.cfg.Password))
	b.WriteString("\n")
	b.WriteString(infoStyle.Render("  Allow Insecure: ON"))
	b.WriteString("\n")
	b.WriteString(infoStyle.Render("  ALPN:           h3"))
	b.WriteString("\n\n")

	uriLabel := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("69")).Render("Connection Link")
	b.WriteString(uriLabel)
	b.WriteString("\n\n")

	uri := m.connectionURI()
	uriStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("36"))
	b.WriteString(uriStyle.Render(uri))
	b.WriteString("\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2).
		Width(60).
		Render(b.String())

	var result strings.Builder
	result.WriteString("\n")
	result.WriteString(centerBox(box, m.width))
	result.WriteString("\n\n")

	qr := m.qrCode()
	if qr != "" {
		qrLabel := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("69")).Render("QR Code")
		result.WriteString(centerBox(qrLabel, m.width))
		result.WriteString("\n\n")
		qrLines := strings.Split(qr, "\n")
		for _, line := range qrLines {
			result.WriteString(centerBox("  "+line, m.width))
			result.WriteString("\n")
		}
		result.WriteString("\n")
	}

	quit := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("36")).
		Render("  Press Enter or q to quit")
	result.WriteString(centerBox(quit, m.width))

	return result.String()
}

func centerBox(box string, width int) string {
	if width <= 0 {
		return box
	}
	return lipgloss.NewStyle().
		Width(width).
		Align(lipgloss.Center).
		Render(box)
}

func (m model) stageReport() string {
	var b strings.Builder
	for _, t := range m.tasks {
		label := "pending"
		switch t.status {
		case taskDone:
			label = "done"
		case taskError:
			label = "failed"
		case taskSkipped:
			label = "skipped"
		case taskRunning:
			label = "running"
		}
		fmt.Fprintf(&b, "[%s] %s", label, t.name)
		if t.reason != "" {
			fmt.Fprintf(&b, " (%s)", t.reason)
		}
		b.WriteString("\n")
		if t.err != nil {
			fmt.Fprintf(&b, "%v\n", t.err)
		}
	}
	return b.String()
}

func (m model) report() string {
	title := "Installation Complete"
	if m.errMsg != "" {
		title = "Installation Failed"
	} else if !m.allTasksDone() {
		title = "Installation Interrupted"
	}
	text := title + "\n\n" + m.stageReport()
	if m.errMsg != "" {
		// Include errors not associated with a stage as well.
		found := false
		for _, t := range m.tasks {
			if t.err != nil {
				found = true
			}
		}
		if !found {
			text += m.errMsg + "\n"
		}
	}
	if m.errMsg == "" && m.allTasksDone() {
		text += fmt.Sprintf("\nServer: %s\nPort: %s\nPassword: %s\nMasquerade: %t (%s)\nBandwidth: ↑%s ↓%s\nClient: Hysteria 2, Allow Insecure: ON, ALPN: h3\nConnection Link: %s\n\n%s", m.serverIP, m.cfg.Port, m.cfg.Password, m.cfg.MasqueradeEnabled, m.cfg.MasqueradeURL, m.cfg.BandwidthUp, m.cfg.BandwidthDown, m.connectionURI(), m.qrCode())
	}
	return text
}

func printFinalReport(w io.Writer, final tea.Model, runErr error) error {
	m, ok := final.(model)
	if ok && len(m.tasks) > 0 {
		if runErr != nil && m.errMsg == "" {
			m.errMsg = runErr.Error()
		}
		if _, err := fmt.Fprintln(w, m.report()); err != nil {
			return err
		}
		if runErr == nil && m.errMsg != "" {
			return fmt.Errorf("installation failed: %s", m.errMsg)
		}
	}
	return runErr
}

func Run() error {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	final, err := p.Run()
	return printFinalReport(os.Stdout, final, err)
}

func RunWithSignals(sigCh <-chan os.Signal) error {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-sigCh:
			p.Quit()
		case <-done:
		}
	}()
	final, err := p.Run()
	return printFinalReport(os.Stdout, final, err)
}
