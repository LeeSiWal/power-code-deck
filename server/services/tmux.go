package services

import (
	"fmt"
	"os/exec"
	"strings"
)

type TmuxService struct{}

func NewTmuxService() *TmuxService {
	return &TmuxService{}
}

func (s *TmuxService) CreateSession(sessionName, workingDir, command string, args []string) error {
	fullCmd := command
	if len(args) > 0 {
		fullCmd += " " + strings.Join(args, " ")
	}

	cmd := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "-c", workingDir, fullCmd)
	cmd.Env = append(cmd.Environ(),
		"LANG=ko_KR.UTF-8",
		"LC_ALL=ko_KR.UTF-8",
	)
	if err := cmd.Run(); err != nil {
		return err
	}

	// Disable alternate screen: strip smcup/rmcup so TUI apps stay in normal buffer.
	// This is the modern tmux 3.x way (alternate-screen option was removed).
	// smcup@:rmcup@ = remove "enter/exit alternate screen" terminal capabilities.
	exec.Command("tmux", "set-option", "-t", sessionName, "terminal-overrides", "xterm*:smcup@:rmcup@").Run()

	// Disable tmux mouse mode so wheel events go to xterm.js, not tmux.
	exec.Command("tmux", "set-option", "-t", sessionName, "mouse", "off").Run()

	return nil
}

func (s *TmuxService) KillSession(sessionName string) error {
	cmd := exec.Command("tmux", "kill-session", "-t", sessionName)
	return cmd.Run()
}

func (s *TmuxService) HasSession(sessionName string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", sessionName)
	return cmd.Run() == nil
}

func (s *TmuxService) ListSessions() ([]string, error) {
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var sessions []string
	for _, line := range lines {
		if line != "" {
			sessions = append(sessions, line)
		}
	}
	return sessions, nil
}

func (s *TmuxService) SendKeys(sessionName, keys string) error {
	cmd := exec.Command("tmux", "send-keys", "-t", sessionName, keys, "Enter")
	return cmd.Run()
}

func (s *TmuxService) CapturePane(sessionName string) (string, error) {
	cmd := exec.Command("tmux", "capture-pane", "-t", sessionName, "-p", "-S", "-100")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (s *TmuxService) GenerateSessionName(prefix string) string {
	return fmt.Sprintf("pcd-%s", prefix)
}
