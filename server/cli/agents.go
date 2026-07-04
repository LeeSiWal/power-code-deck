package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func cmdList() {
	token := requireToken()
	resp, err := apiGet("/api/agents", token)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	var agents []map[string]interface{}
	json.Unmarshal(resp, &agents)

	if len(agents) == 0 {
		fmt.Println("No agents running.")
		return
	}

	for _, a := range agents {
		fmt.Printf("  %s  %-20s  %-8s  %s\n", a["id"], a["name"], a["status"], a["workingDir"])
	}
}

func cmdCreate(args []string) {
	token := requireToken()
	flags := parseFlags(args)

	preset := flags["preset"]
	if preset == "" {
		preset = "claude-code"
	}
	dir := flags["dir"]
	if dir == "" {
		dir, _ = os.Getwd()
	}
	name := flags["name"]
	if name == "" {
		name = fmt.Sprintf("%s agent", preset)
	}

	body := fmt.Sprintf(`{"preset":"%s","name":"%s","workingDir":"%s","command":"%s","args":[]}`, preset, name, dir, preset)
	resp, err := apiPost("/api/agents", token, body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	var agent map[string]interface{}
	json.Unmarshal(resp, &agent)
	fmt.Printf("Created agent: %s (%s)\n", agent["id"], agent["name"])
}

func cmdDelete(args []string) {
	token := requireToken()
	pos := positionalArgs(args)
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: pcd delete <id>")
		os.Exit(1)
	}
	_, err := apiDelete("/api/agents/"+pos[0], token)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	fmt.Println("Deleted.")
}

func cmdSend(args []string) {
	token := requireToken()
	pos := positionalArgs(args)
	if len(pos) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: pcd send <id> <text>")
		os.Exit(1)
	}
	id := pos[0]
	text := strings.Join(pos[1:], " ")

	flags := parseFlags(args)
	if flags["ctrl-c"] == "true" {
		text = "\x03"
	}

	body := fmt.Sprintf(`{"data":"%s\n"}`, strings.ReplaceAll(text, `"`, `\"`))
	_, err := apiPost("/api/agents/"+id+"/send", token, body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	fmt.Println("Sent.")
}

func cmdStatus(args []string) {
	token := requireToken()
	pos := positionalArgs(args)
	if len(pos) > 0 {
		resp, err := apiGet("/api/agents/"+pos[0]+"/meta", token)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		fmt.Println(string(resp))
		return
	}
	cmdList()
}

func cmdPing() {
	resp, err := http.Get(serverURL + "/api/auth/health")
	if err != nil {
		fmt.Println("Server is not running.")
		os.Exit(1)
	}
	defer resp.Body.Close()
	fmt.Println("Server is running.")
}

func cmdOpen() {
	url := serverURL
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		cmd = exec.Command("cmd", "/c", "start", url)
	}
	cmd.Start()
	fmt.Println("Opening browser...")
}

func apiGet(path, token string) ([]byte, error) {
	req, _ := http.NewRequest("GET", serverURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func apiPost(path, token, body string) ([]byte, error) {
	req, _ := http.NewRequest("POST", serverURL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func apiDelete(path, token string) ([]byte, error) {
	req, _ := http.NewRequest("DELETE", serverURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
