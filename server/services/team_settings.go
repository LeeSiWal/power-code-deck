package services

import (
	"database/sql"
	"encoding/json"
)

const teamSettingsKey = "team_settings"

// TeamMember is one CLI a session can call as a read-only team member.
type TeamMember struct {
	ID      string `json:"id"`
	Adapter string `json:"adapter"`
	Model   string `json:"model"`
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
}

// TeamSettings is the global dream team configuration.
type TeamSettings struct {
	Members         []TeamMember `json:"members"`
	ResultMaxChars  int          `json:"resultMaxChars"`
	MaxCallsPerTurn int          `json:"maxCallsPerTurn"`
	SessionTokenCap int          `json:"sessionTokenCap"`
	TimeoutSec      int          `json:"timeoutSec"`
}

func defaultTeamMembers() []TeamMember {
	return []TeamMember{
		{ID: "codex", Adapter: "codex", Model: "", Label: "Codex", Enabled: true},
		{ID: "claude-sonnet", Adapter: "claude", Model: "claude-sonnet-5-5", Label: "Claude Sonnet", Enabled: true},
		{ID: "claude-haiku", Adapter: "claude", Model: "claude-haiku-5-5", Label: "Claude Haiku", Enabled: true},
		{ID: "claude-fable", Adapter: "claude", Model: "claude-fable-5-2", Label: "Claude Fable", Enabled: false},
	}
}

// DefaultTeamSettings returns the built-in team and limits.
func DefaultTeamSettings() TeamSettings {
	return TeamSettings{
		Members:         defaultTeamMembers(),
		ResultMaxChars:  6000,
		MaxCallsPerTurn: 3,
		SessionTokenCap: 300000,
		TimeoutSec:      180,
	}
}

// NormalizeTeamSettings drops unknown members, appends missing default ones and
// replaces non-positive limits with the defaults.
func NormalizeTeamSettings(s TeamSettings) TeamSettings {
	def := DefaultTeamSettings()
	known := map[string]bool{}
	for _, m := range def.Members {
		known[m.ID] = true
	}
	seen := map[string]bool{}
	var members []TeamMember
	for _, m := range s.Members {
		if !known[m.ID] || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		members = append(members, m)
	}
	for _, m := range def.Members {
		if !seen[m.ID] {
			members = append(members, m)
		}
	}
	// Adapter/model/label are fixed by the server; only the toggle is the user's.
	for i, m := range members {
		for _, d := range def.Members {
			if d.ID == m.ID {
				d.Enabled = m.Enabled
				members[i] = d
			}
		}
	}
	s.Members = members
	if s.ResultMaxChars <= 0 {
		s.ResultMaxChars = def.ResultMaxChars
	}
	if s.MaxCallsPerTurn <= 0 {
		s.MaxCallsPerTurn = def.MaxCallsPerTurn
	}
	if s.SessionTokenCap <= 0 {
		s.SessionTokenCap = def.SessionTokenCap
	}
	if s.TimeoutSec <= 0 {
		s.TimeoutSec = def.TimeoutSec
	}
	return s
}

// LoadTeamSettings reads the stored settings; missing or bad JSON gives defaults.
func LoadTeamSettings(db *sql.DB) TeamSettings {
	var raw string
	if db.QueryRow("SELECT value FROM app_config WHERE key = ?", teamSettingsKey).Scan(&raw) != nil {
		return DefaultTeamSettings()
	}
	var s TeamSettings
	if json.Unmarshal([]byte(raw), &s) != nil {
		return DefaultTeamSettings()
	}
	return NormalizeTeamSettings(s)
}

// SaveTeamSettings stores the settings (normalized) in app_config.
func SaveTeamSettings(db *sql.DB, s TeamSettings) error {
	b, err := json.Marshal(NormalizeTeamSettings(s))
	if err != nil {
		return err
	}
	_, err = db.Exec(
		"INSERT INTO app_config(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
		teamSettingsKey, string(b),
	)
	return err
}
