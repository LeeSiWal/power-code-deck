package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func tokenPath() string {
	var dir string
	switch runtime.GOOS {
	case "darwin":
		dir = filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "powercodedeck")
	case "windows":
		dir = filepath.Join(os.Getenv("APPDATA"), "powercodedeck")
	default:
		dir = filepath.Join(os.Getenv("HOME"), ".config", "powercodedeck")
	}
	os.MkdirAll(dir, 0700)
	return filepath.Join(dir, "token.json")
}

func loadToken() string {
	data, err := os.ReadFile(tokenPath())
	if err != nil {
		return ""
	}
	var tok struct {
		AccessToken string `json:"accessToken"`
	}
	json.Unmarshal(data, &tok)
	return tok.AccessToken
}

func saveToken(access, refresh string) error {
	data, _ := json.Marshal(map[string]string{
		"accessToken":  access,
		"refreshToken": refresh,
	})
	path := tokenPath()
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	return nil
}

func cmdLogin() {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Enter PIN: ")
	pin, _ := reader.ReadString('\n')
	pin = strings.TrimSpace(pin)

	body := fmt.Sprintf(`{"pin":"%s"}`, pin)
	resp, err := apiPost("/api/auth/login", "", body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Login failed:", err)
		os.Exit(1)
	}

	var tokens struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.Unmarshal(resp, &tokens); err != nil || tokens.AccessToken == "" {
		fmt.Fprintln(os.Stderr, "Login failed: invalid PIN")
		os.Exit(1)
	}

	if err := saveToken(tokens.AccessToken, tokens.RefreshToken); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to save token:", err)
		os.Exit(1)
	}
	fmt.Println("Logged in successfully.")
}
