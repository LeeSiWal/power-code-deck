package config

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"agentdeck/auth"
	"agentdeck/version"

	"github.com/joho/godotenv"
)

type Config struct {
	AuthEnabled   bool   // whether PowerCodeDeck enforces its own auth
	AuthMethod    string // "none" | "pin" | "password"
	Pin           string
	PasswordHash  string
	JWTSecret     string
	Port          string
	DBPath        string
	CORSOrigins   string
	WorkspaceRoot string // default root for the project browser (optional)
	SetupMode     bool   // true when this run performed first-run setup
}

// Load reads configuration from the environment / .env file.
//
// Environment variables use the POWERCODEDECK_ prefix; the legacy AGENTDECK_
// prefix remains supported for backward compatibility. When both are present
// for the same setting, POWERCODEDECK_ wins.
//
// On a truly unconfigured first run it launches an interactive setup wizard
// (when stdin is a TTY) so the user can pick none/pin/password. Default is
// no authentication. Non-interactive first runs silently default to no auth.
func Load() *Config {
	envPath := findEnvFile()
	if envPath != "" {
		godotenv.Load(envPath)
	}

	cfg := &Config{
		Pin:           envDual("PIN"),
		PasswordHash:  envDual("PASSWORD_HASH"),
		JWTSecret:     envDual("JWT_SECRET"),
		Port:          envDual("PORT"),
		DBPath:        envDual("DB_PATH"),
		CORSOrigins:   envDual("CORS_ORIGINS"),
		WorkspaceRoot: envDual("WORKSPACE_ROOT"),
	}

	authEnabledStr, authEnabledSet := envDualLookup("AUTH_ENABLED")
	authMethod := envDual("AUTH_METHOD")

	switch {
	case authEnabledSet || authMethod != "":
		// Explicitly configured (new or legacy). Derive method + enabled.
		cfg.AuthMethod = deriveMethod(authMethod, cfg.Pin, cfg.PasswordHash)
		if authEnabledSet {
			cfg.AuthEnabled = parseBool(authEnabledStr)
		} else {
			cfg.AuthEnabled = cfg.AuthMethod != "none"
		}

	case cfg.Pin != "" || cfg.PasswordHash != "":
		// Back-compat: legacy .env has AGENTDECK_PIN (or a hash) but no explicit
		// auth flags → treat as enabled auth of the matching method.
		cfg.AuthMethod = deriveMethod("", cfg.Pin, cfg.PasswordHash)
		cfg.AuthEnabled = true

	default:
		// Truly unconfigured → first run.
		cfg.SetupMode = true
		runFirstRunWizard(cfg)
	}

	// Auth needs a JWT signing secret; generate + persist if missing.
	if cfg.AuthEnabled && cfg.JWTSecret == "" {
		cfg.JWTSecret = randomHex(32)
		updateEnvValue("POWERCODEDECK_JWT_SECRET", cfg.JWTSecret)
	}
	// No-auth mode still needs an in-memory secret for anonymous tokens.
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = randomHex(32)
	}

	if cfg.AuthMethod == "" {
		cfg.AuthMethod = "none"
	}
	if cfg.Port == "" {
		cfg.Port = "33033"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = resolveDBPath()
	}
	if cfg.CORSOrigins == "" {
		cfg.CORSOrigins = fmt.Sprintf("http://localhost:%s", cfg.Port)
	}

	return cfg
}

// deriveMethod resolves the auth method from an explicit value, falling back to
// the presence of a PIN or password hash.
func deriveMethod(explicit, pin, passwordHash string) string {
	switch explicit {
	case "none", "pin", "password":
		return explicit
	}
	if pin != "" {
		return "pin"
	}
	if passwordHash != "" {
		return "password"
	}
	return "none"
}

// runFirstRunWizard prompts the user to choose an auth method on first run.
// Non-interactive launches (PM2, .app bundle, no TTY) default to no auth.
func runFirstRunWizard(cfg *Config) {
	cfg.AuthMethod = "none"
	cfg.AuthEnabled = false

	if !isInteractive() {
		saveEnvFile(cfg)
		return
	}

	fmt.Println()
	fmt.Printf("  %s %s first run\n", version.AppName, "v"+version.Version)
	fmt.Println("  ------------------------------------------------")
	fmt.Println("  인증을 사용할까요?  (Choose authentication)")
	fmt.Println("    [1] 사용 안 함 / none   (기본값, default)")
	fmt.Println("    [2] PIN 사용 / pin")
	fmt.Println("    [3] 비밀번호 사용 / password")
	fmt.Print("  선택 (Enter = 1): ")

	reader := bufio.NewReader(os.Stdin)
	choice, _ := reader.ReadString('\n')
	switch strings.TrimSpace(choice) {
	case "2", "pin":
		if pin := promptPin(reader); pin != "" {
			cfg.AuthMethod = "pin"
			cfg.AuthEnabled = true
			cfg.Pin = pin
			cfg.JWTSecret = randomHex(32)
		}
	case "3", "password":
		if hash := promptPassword(reader); hash != "" {
			cfg.AuthMethod = "password"
			cfg.AuthEnabled = true
			cfg.PasswordHash = hash
			cfg.JWTSecret = randomHex(32)
		}
	default:
		// no auth
	}

	saveEnvFile(cfg)
}

func promptPin(reader *bufio.Reader) string {
	fmt.Print("  PIN을 입력하세요 (숫자 6자리 권장): ")
	pin, _ := reader.ReadString('\n')
	return strings.TrimSpace(pin)
}

func promptPassword(reader *bufio.Reader) string {
	fmt.Print("  비밀번호를 입력하세요: ")
	p1, _ := reader.ReadString('\n')
	p1 = strings.TrimSpace(p1)
	fmt.Print("  비밀번호를 다시 입력하세요: ")
	p2, _ := reader.ReadString('\n')
	p2 = strings.TrimSpace(p2)
	if p1 == "" || p1 != p2 {
		fmt.Println("  ⚠ 비밀번호가 비어 있거나 일치하지 않아 인증 없음으로 설정합니다.")
		return ""
	}
	return auth.HashPassword(p1)
}

// envDual returns the POWERCODEDECK_<name> value, falling back to AGENTDECK_<name>.
func envDual(name string) string {
	v, _ := envDualLookup(name)
	return v
}

// envDualLookup reports the resolved value and whether either prefix was set.
func envDualLookup(name string) (string, bool) {
	if v, ok := os.LookupEnv("POWERCODEDECK_" + name); ok && v != "" {
		return v, true
	}
	if v, ok := os.LookupEnv("AGENTDECK_" + name); ok && v != "" {
		return v, true
	}
	return "", false
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func findEnvFile() string {
	// 1. Next to executable
	exe, err := os.Executable()
	if err == nil {
		p := filepath.Join(filepath.Dir(exe), ".env")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// 2. Current directory
	if _, err := os.Stat(".env"); err == nil {
		return ".env"
	}
	return ""
}

func resolveDBPath() string {
	// Keep the legacy filename so existing installs keep their data.
	exe, err := os.Executable()
	if err == nil {
		return filepath.Join(filepath.Dir(exe), "agentdeck.db")
	}
	return "./agentdeck.db"
}

func envFilePath() string {
	if p := findEnvFile(); p != "" {
		return p
	}
	exe, err := os.Executable()
	if err == nil {
		return filepath.Join(filepath.Dir(exe), ".env")
	}
	return ".env"
}

// saveEnvFile writes the resolved configuration using the new POWERCODEDECK_
// prefix. PIN / password hash are only written when auth is enabled.
func saveEnvFile(cfg *Config) {
	var b strings.Builder
	b.WriteString("# PowerCodeDeck configuration\n")
	b.WriteString("# POWERCODEDECK_* is preferred; legacy AGENTDECK_* still works.\n")
	fmt.Fprintf(&b, "POWERCODEDECK_AUTH_ENABLED=%t\n", cfg.AuthEnabled)
	fmt.Fprintf(&b, "POWERCODEDECK_AUTH_METHOD=%s\n", cfg.AuthMethod)
	fmt.Fprintf(&b, "POWERCODEDECK_PIN=%s\n", cfg.Pin)
	fmt.Fprintf(&b, "POWERCODEDECK_PASSWORD_HASH=%s\n", cfg.PasswordHash)
	fmt.Fprintf(&b, "POWERCODEDECK_JWT_SECRET=%s\n", cfg.JWTSecret)
	fmt.Fprintf(&b, "POWERCODEDECK_PORT=%s\n", cfg.Port)
	fmt.Fprintf(&b, "POWERCODEDECK_DB_PATH=%s\n", cfg.DBPath)
	fmt.Fprintf(&b, "POWERCODEDECK_CORS_ORIGINS=%s\n", cfg.CORSOrigins)
	fmt.Fprintf(&b, "POWERCODEDECK_WORKSPACE_ROOT=%s\n", cfg.WorkspaceRoot)

	os.WriteFile(envFilePath(), []byte(b.String()), 0600)
}

// updateEnvValue upserts a single key in the existing .env without clobbering
// other user-set values. Preserves comments/order is not required here.
func updateEnvValue(key, value string) {
	envPath := findEnvFile()
	if envPath == "" {
		return
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		return
	}
	env, _ := godotenv.Unmarshal(string(data))
	if env == nil {
		env = map[string]string{}
	}
	env[key] = value
	var b strings.Builder
	for k, v := range env {
		fmt.Fprintf(&b, "%s=%s\n", k, v)
	}
	os.WriteFile(envPath, []byte(b.String()), 0600)
	log.Printf("Updated %s in %s", key, envPath)
}
