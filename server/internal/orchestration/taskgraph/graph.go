// Package taskgraph validates provider-neutral task plans and selects runnable
// tasks. It performs no I/O and never treats provider completion as verification.
package taskgraph

import (
	"fmt"
	"regexp"
	"strings"
)

type Task struct {
	ID        string   `json:"id"`
	Prompt    string   `json:"prompt"`
	Provider  string   `json:"provider"`
	DependsOn []string `json:"dependsOn"`
}

type State string

const (
	Pending   State = "pending"
	Running   State = "running"
	Verifying State = "verifying"
	Succeeded State = "succeeded"
	Failed    State = "failed"
	Canceled  State = "canceled"
)

type Graph struct {
	tasks []Task
	index map[string]int
}

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// New accepts at most 64 tasks and 256 KiB of prompts. Input is copied so a
// planner cannot mutate a validated graph after it has been accepted.
func New(tasks []Task) (*Graph, error) {
	if len(tasks) == 0 || len(tasks) > 64 {
		return nil, fmt.Errorf("plan must contain 1 to 64 tasks")
	}
	g := &Graph{index: map[string]int{}}
	total := 0
	for _, task := range tasks {
		if !validID.MatchString(task.ID) {
			return nil, fmt.Errorf("invalid task ID %q", task.ID)
		}
		if _, exists := g.index[task.ID]; exists {
			return nil, fmt.Errorf("duplicate task %q", task.ID)
		}
		if task.Provider != "claude" && task.Provider != "codex" && task.Provider != "antigravity" {
			return nil, fmt.Errorf("unsupported provider for %q", task.ID)
		}
		if strings.TrimSpace(task.Prompt) == "" || len(task.Prompt) > 65536 {
			return nil, fmt.Errorf("invalid prompt for %q", task.ID)
		}
		total += len(task.Prompt)
		if total > 256*1024 || len(task.DependsOn) > 63 {
			return nil, fmt.Errorf("plan exceeds size limits")
		}
		task.DependsOn = append([]string{}, task.DependsOn...)
		g.index[task.ID] = len(g.tasks)
		g.tasks = append(g.tasks, task)
	}
	for _, task := range g.tasks {
		seen := map[string]bool{}
		for _, id := range task.DependsOn {
			if _, exists := g.index[id]; !exists || id == task.ID || seen[id] {
				return nil, fmt.Errorf("invalid dependency %q for %q", id, task.ID)
			}
			seen[id] = true
		}
	}
	color := make([]int, len(tasks))
	var visit func(int) error
	visit = func(i int) error {
		if color[i] == 1 {
			return fmt.Errorf("dependency cycle at %q", g.tasks[i].ID)
		}
		if color[i] == 2 {
			return nil
		}
		color[i] = 1
		for _, id := range g.tasks[i].DependsOn {
			if err := visit(g.index[id]); err != nil {
				return err
			}
		}
		color[i] = 2
		return nil
	}
	for i := range g.tasks {
		if err := visit(i); err != nil {
			return nil, err
		}
	}
	return g, nil
}

type Selection struct {
	Ready    []Task   `json:"ready"`
	Blocked  []string `json:"blocked"`
	Complete bool     `json:"complete"`
}

// Select preserves input order. Running and verifying tasks both occupy slots;
// only verified success releases dependencies. Failed ancestors block children
// transitively. State is server-owned; callers must claim selected tasks atomically.
func (g *Graph) Select(states map[string]State, concurrency int) (Selection, error) {
	out := Selection{Ready: []Task{}, Blocked: []string{}, Complete: true}
	if concurrency < 1 || concurrency > 64 {
		return out, fmt.Errorf("concurrency must be 1 to 64")
	}
	active := 0
	for id, state := range states {
		if _, exists := g.index[id]; !exists {
			return out, fmt.Errorf("unknown task state %q", id)
		}
		switch state {
		case Running, Verifying:
			active++
		case Pending, Succeeded, Failed, Canceled:
		default:
			return out, fmt.Errorf("unknown state %q", state)
		}
	}
	// Cache reachability so layered graphs do not cause exponential recursion.
	memo := map[int]bool{}
	var isBlocked func(int) bool
	isBlocked = func(i int) bool {
		if value, ok := memo[i]; ok {
			return value
		}
		for _, dep := range g.tasks[i].DependsOn {
			if states[dep] == Failed || states[dep] == Canceled || isBlocked(g.index[dep]) {
				memo[i] = true
				return true
			}
		}
		memo[i] = false
		return false
	}
	for i, task := range g.tasks {
		state := states[task.ID]
		if state != Succeeded {
			out.Complete = false
		}
		if state != "" && state != Pending {
			continue
		}
		if isBlocked(i) {
			out.Blocked = append(out.Blocked, task.ID)
			continue
		}
		ready := true
		for _, id := range task.DependsOn {
			if states[id] != Succeeded {
				ready = false
				break
			}
		}
		if ready && active+len(out.Ready) < concurrency {
			task.DependsOn = append([]string{}, task.DependsOn...)
			out.Ready = append(out.Ready, task)
		}
	}
	return out, nil
}
