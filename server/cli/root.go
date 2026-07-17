package cli

import (
	"fmt"
	"os"
	"strings"

	"powercodedeck/version"
)

var serverURL = resolveServerURL()

func resolveServerURL() string {
	port := os.Getenv("POWERCODEDECK_PORT")
	if port == "" {
		port = os.Getenv("AGENTDECK_PORT")
	}
	if port == "" {
		port = "33033"
	}
	return "http://localhost:" + port
}

func Run(args []string) {
	if len(args) == 0 {
		return
	}

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "list":
		cmdList()
	case "create":
		cmdCreate(rest)
	case "delete":
		cmdDelete(rest)
	case "send":
		cmdSend(rest)
	case "status":
		cmdStatus(rest)
	case "login":
		cmdLogin()
	case "open":
		cmdOpen()
	case "ping":
		cmdPing()
	case "mcp-approve":
		// Not user-facing: Claude Code spawns this as its MCP permission server.
		// See cli/mcp_approve.go. Deliberately absent from printHelp().
		cmdMCPApprove()
	case "--version", "version":
		fmt.Printf("%s v%s\n", version.Binary, version.Version)
	case "--help", "help":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printHelp()
		os.Exit(1)
	}
	os.Exit(0)
}

func IsSubcommand(args []string) bool {
	if len(args) < 2 {
		return false
	}
	cmds := []string{"list", "create", "delete", "send", "status", "login", "open", "ping", "mcp-approve", "version", "help", "--version", "--help"}
	for _, c := range cmds {
		if args[1] == c {
			return true
		}
	}
	return false
}

func printHelp() {
	help := `PowerCodeDeck - AI Coding Terminal Console

Usage: pcd [command]

Commands:
  (no command)     Start the server
  list             List agents
  create           Create a new agent
  delete <id>      Delete an agent
  send <id> <text> Send text to an agent
  status [id]      Show agent status
  login            Authenticate CLI
  open             Open browser
  ping             Check server status
  version          Show version
  help             Show this help`
	fmt.Println(help)
}

func requireToken() string {
	token := loadToken()
	if token == "" {
		fmt.Fprintln(os.Stderr, "Not authenticated. Run: pcd login")
		os.Exit(1)
	}
	return token
}

func parseFlags(args []string) map[string]string {
	flags := make(map[string]string)
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			key := strings.TrimPrefix(args[i], "--")
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				flags[key] = args[i+1]
				i++
			} else {
				flags[key] = "true"
			}
		}
	}
	return flags
}

func positionalArgs(args []string) []string {
	var pos []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			i++
			continue
		}
		pos = append(pos, args[i])
	}
	return pos
}
