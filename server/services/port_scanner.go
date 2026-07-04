package services

import (
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

type PortScanner struct {
	mu    sync.RWMutex
	cache map[string][]int
}

func NewPortScanner() *PortScanner {
	return &PortScanner{
		cache: make(map[string][]int),
	}
}

func (s *PortScanner) GetPorts(agentID string) []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cache[agentID]
}

func (s *PortScanner) Poll(agentID string) []int {
	var ports []int

	if runtime.GOOS == "darwin" {
		ports = s.scanDarwin()
	} else {
		ports = s.scanLinux()
	}

	var filtered []int
	for _, p := range ports {
		if p >= 1024 {
			filtered = append(filtered, p)
		}
	}

	s.mu.Lock()
	s.cache[agentID] = filtered
	s.mu.Unlock()

	return filtered
}

var portRegex = regexp.MustCompile(`:(\d+)\s`)

func (s *PortScanner) scanDarwin() []int {
	out, err := exec.Command("lsof", "-iTCP", "-sTCP:LISTEN", "-P", "-n").Output()
	if err != nil {
		return nil
	}
	return parsePortsFromLsof(string(out))
}

func (s *PortScanner) scanLinux() []int {
	out, err := exec.Command("ss", "-tlnp").Output()
	if err != nil {
		return s.scanDarwin()
	}
	return parsePortsFromSS(string(out))
}

func parsePortsFromLsof(output string) []int {
	seen := make(map[int]bool)
	var ports []int
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		addr := fields[8]
		parts := strings.Split(addr, ":")
		if len(parts) < 2 {
			continue
		}
		portStr := parts[len(parts)-1]
		if p, err := strconv.Atoi(portStr); err == nil && !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
	}
	return ports
}

func parsePortsFromSS(output string) []int {
	seen := make(map[int]bool)
	var ports []int
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		addr := fields[3]
		parts := strings.Split(addr, ":")
		if len(parts) < 2 {
			continue
		}
		portStr := parts[len(parts)-1]
		if p, err := strconv.Atoi(portStr); err == nil && !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
	}
	return ports
}

func (s *PortScanner) Remove(agentID string) {
	s.mu.Lock()
	delete(s.cache, agentID)
	s.mu.Unlock()
}
