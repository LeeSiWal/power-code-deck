package routing

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var configFileMu sync.Mutex

// UpdateConfigFile edits routing.json through mutate, which gets the file as
// generic JSON so fields this code does not know survive untouched. The result
// must pass LoadConfig before it replaces the file; the previous file is kept
// as routing.json.bak-<time> (the newest 10 are kept).
func UpdateConfigFile(path string, mutate func(raw map[string]any) error) (Config, error) {
	configFileMu.Lock()
	defer configFileMu.Unlock()
	raw := map[string]any{}
	old, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(old, &raw); err != nil {
			return Config{}, fmt.Errorf("routing.json is not valid JSON: %w", err)
		}
	case errors.Is(err, os.ErrNotExist):
		old = nil
	default:
		return Config{}, err
	}
	if err := mutate(raw); err != nil {
		return Config{}, err
	}
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return Config{}, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0600); err != nil {
		return Config{}, err
	}
	cfg, err := LoadConfig(tmp)
	if err != nil {
		os.Remove(tmp)
		return Config{}, err
	}
	if old != nil {
		if err := os.WriteFile(path+".bak-"+time.Now().Format("20060102-150405"), old, 0600); err != nil {
			os.Remove(tmp)
			return Config{}, fmt.Errorf("backup routing.json: %w", err)
		}
		pruneBackups(path, 10)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return Config{}, err
	}
	return cfg, nil
}

func pruneBackups(path string, keep int) {
	matches, _ := filepath.Glob(path + ".bak-2*") // timestamped ones only
	sort.Strings(matches)
	for len(matches) > keep {
		os.Remove(matches[0])
		matches = matches[1:]
	}
}

// EndpointSet is the live list of local endpoints shared by the Settings API
// and the OSS bridge, so an endpoint added in Settings works without a restart.
type EndpointSet struct {
	v atomic.Pointer[map[string]LocalEndpoint]
}

func (s *EndpointSet) Set(eps []LocalEndpoint) {
	m := make(map[string]LocalEndpoint, len(eps))
	for _, e := range eps {
		m[e.ID] = e
	}
	s.v.Store(&m)
}

// Get returns the endpoints of the given kind ("" = all).
func (s *EndpointSet) Get(kind string) map[string]LocalEndpoint {
	p := s.v.Load()
	out := map[string]LocalEndpoint{}
	if p == nil {
		return out
	}
	for id, e := range *p {
		if kind == "" || strings.EqualFold(e.Kind, kind) {
			out[id] = e
		}
	}
	return out
}
