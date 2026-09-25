package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"powercodedeck/internal/routing"
	"powercodedeck/internal/routing/runroute"
)

// LocalModels is the Settings API for local model servers. Everything it saves
// goes to routing.json (validated first, previous file backed up), then the
// router and the OSS bridge switch to it without a restart. Addresses are
// limited to this machine, private networks and Tailscale (localNetOnly,
// enforced at dial time), so a URL typed in the browser cannot send code to
// the internet.
type LocalModels struct {
	ConfigPath string
	Coord      *runroute.Coordinator
	Endpoints  *routing.EndpointSet
	Client     *routing.LocalClient
}

var localIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

type localModelView struct {
	ID         string   `json:"id"`
	URL        string   `json:"url"`
	Kind       string   `json:"kind"`
	Model      string   `json:"model"`
	Status     string   `json:"status"` // installed | unknown
	Detail     string   `json:"detail,omitempty"`
	Models     []string `json:"models"`
	UseForEasy bool     `json:"useForEasy"`
	UseAsJudge bool     `json:"useAsJudge"`
	ProfileID  string   `json:"profileId,omitempty"`
	Tiers      []string `json:"tiers,omitempty"`
}

func (l *LocalModels) Register(api *mux.Router) {
	api.HandleFunc("/v2/routing/local", l.list).Methods("GET")
	api.HandleFunc("/v2/routing/local/test", l.test).Methods("POST")
	api.HandleFunc("/v2/routing/local/{id}", l.save).Methods("PUT")
	api.HandleFunc("/v2/routing/local/{id}", l.remove).Methods("DELETE")
}

func (l *LocalModels) list(w http.ResponseWriter, r *http.Request) {
	snap := l.Coord.Snapshot(r.Context())
	cfg, err := routing.LoadConfig(l.ConfigPath)
	if err != nil {
		cfg = routing.Config{}
	}
	status := map[string]routing.AdapterStatus{}
	for _, a := range snap.Adapters {
		status[a.AdapterID] = a
	}
	out := []localModelView{}
	for _, e := range cfg.LocalEndpoints {
		v := localModelView{ID: e.ID, URL: e.URL, Kind: e.Kind, Model: e.Model, Status: "unknown", Models: []string{}}
		if st, ok := status["local:"+e.ID]; ok {
			v.Status, v.Detail = string(st.Installation.Value), st.Installation.Detail
			for _, m := range st.Models {
				v.Models = append(v.Models, m.ID)
			}
		}
		for _, p := range cfg.Profiles {
			if p.IsLocalCodex() && p.EndpointRef == e.ID {
				v.UseForEasy, v.ProfileID = p.Allow, p.ID
				for _, t := range p.Tiers {
					v.Tiers = append(v.Tiers, t.String())
				}
			}
		}
		v.UseAsJudge = cfg.TierJudge != nil && cfg.TierJudge.EndpointRef == e.ID
		out = append(out, v)
	}
	jsonResponse(w, map[string]any{"endpoints": out})
}

type localModelInput struct {
	URL        string `json:"url"`
	Kind       string `json:"kind"`
	Model      string `json:"model"`
	UseForEasy bool   `json:"useForEasy"`
	UseAsJudge bool   `json:"useAsJudge"`
}

// endpointFor builds the endpoint a Settings entry stands for, with the
// local-network-only policy always on.
func endpointFor(id string, in localModelInput) (routing.LocalEndpoint, error) {
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if kind == "" {
		kind = "openai"
	}
	e := routing.LocalEndpoint{ID: id, URL: strings.TrimRight(strings.TrimSpace(in.URL), "/"), Kind: kind, Model: strings.TrimSpace(in.Model),
		AllowPrivate: true, AllowInsecureHTTP: true, LocalNetOnly: true, TimeoutSeconds: 300}
	return e, koreanAddrError(e.Validate())
}

// koreanAddrError words the address-policy refusals for the Settings screen.
func koreanAddrError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "only this machine, private networks and Tailscale"):
		return fmt.Errorf("이 기기, 사설망(192.168.x.x·10.x.x.x 등), 테일스케일(100.x.x.x) 주소만 연결할 수 있습니다")
	case strings.Contains(msg, "invalid url"), strings.Contains(msg, "scheme must be"):
		return fmt.Errorf("주소 형식이 올바르지 않습니다. 예: http://192.168.1.22:8080")
	case strings.Contains(msg, "metadata"), strings.Contains(msg, "link-local"):
		return fmt.Errorf("이 주소에는 연결할 수 없습니다")
	}
	return err
}

func (l *LocalModels) test(w http.ResponseWriter, r *http.Request) {
	var in localModelInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&in); err != nil {
		jsonError(w, "invalid request", http.StatusBadRequest)
		return
	}
	e, err := endpointFor("test", in)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	models, err := l.Client.Models(ctx, e)
	if err != nil {
		if k := koreanAddrError(err); k != err {
			jsonError(w, k.Error(), http.StatusBadRequest)
			return
		}
		jsonError(w, "연결하지 못했습니다: "+err.Error(), http.StatusBadGateway)
		return
	}
	ids := []string{}
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	jsonResponse(w, map[string]any{"models": ids})
}

func (l *LocalModels) save(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if !localIDRe.MatchString(id) {
		jsonError(w, "이름은 영문 소문자·숫자·하이픈(-)으로 32자 이내여야 합니다", http.StatusBadRequest)
		return
	}
	var in localModelInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&in); err != nil {
		jsonError(w, "invalid request", http.StatusBadRequest)
		return
	}
	e, err := endpointFor(id, in)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if in.UseForEasy && (e.Kind != "openai" || e.Model == "") {
		jsonError(w, "쉬운 작업 처리에는 OpenAI 호환 형식과 모델 선택이 필요합니다", http.StatusBadRequest)
		return
	}
	cfg, err := routing.UpdateConfigFile(l.ConfigPath, func(raw map[string]any) error {
		setEndpoint(raw, e)
		setLocalProfile(raw, e, in.UseForEasy)
		setJudge(raw, id, in.UseAsJudge)
		return nil
	})
	if err != nil {
		jsonError(w, "저장하지 못했습니다: "+err.Error(), http.StatusBadRequest)
		return
	}
	l.apply(cfg)
	l.list(w, r)
}

func (l *LocalModels) remove(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	cfg, err := routing.UpdateConfigFile(l.ConfigPath, func(raw map[string]any) error {
		raw["localEndpoints"] = filterList(raw["localEndpoints"], func(m map[string]any) bool { return m["id"] != id })
		// Profiles on it would no longer validate; sessions that used one simply
		// route elsewhere on their next turn.
		raw["profiles"] = filterList(raw["profiles"], func(m map[string]any) bool { return m["endpointRef"] != id })
		setJudge(raw, id, false)
		return nil
	})
	if err != nil {
		jsonError(w, "삭제하지 못했습니다: "+err.Error(), http.StatusBadRequest)
		return
	}
	l.apply(cfg)
	w.WriteHeader(http.StatusNoContent)
}

func (l *LocalModels) apply(cfg routing.Config) {
	l.Coord.Reload(cfg)
	if l.Endpoints != nil {
		l.Endpoints.Set(cfg.LocalEndpoints)
	}
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func filterList(v any, keep func(map[string]any) bool) []any {
	out := []any{}
	for _, x := range asList(v) {
		if m, ok := x.(map[string]any); !ok || keep(m) {
			out = append(out, x)
		}
	}
	return out
}

func setEndpoint(raw map[string]any, e routing.LocalEndpoint) {
	b, _ := json.Marshal(e)
	var m map[string]any
	json.Unmarshal(b, &m)
	list := asList(raw["localEndpoints"])
	for i, x := range list {
		if old, ok := x.(map[string]any); ok && old["id"] == e.ID {
			list[i] = m
			raw["localEndpoints"] = list
			return
		}
	}
	raw["localEndpoints"] = append(list, m)
}

// setLocalProfile keeps one local Codex profile per endpoint: turning "easy
// work" off keeps it with allow=false (sessions that reference it stay valid),
// turning it on creates it (VERY_EASY, local billing) or re-enables it.
func setLocalProfile(raw map[string]any, e routing.LocalEndpoint, on bool) {
	list := asList(raw["profiles"])
	for _, x := range list {
		p, ok := x.(map[string]any)
		if !ok || p["adapter"] != "codex" || p["endpointRef"] != e.ID {
			continue
		}
		p["allow"] = on
		if e.Model != "" {
			p["model"] = e.Model
		}
		raw["profiles"] = list
		return
	}
	if !on {
		return
	}
	p := map[string]any{"id": "local-" + e.ID, "adapter": "codex", "endpointRef": e.ID, "model": e.Model,
		"billing": "local", "tiers": []any{"VERY_EASY"}, "allow": true}
	// First in the list: with equal scores the earlier profile wins a tie.
	raw["profiles"] = append([]any{p}, list...)
}

func setJudge(raw map[string]any, id string, on bool) {
	cur, _ := raw["tierJudge"].(map[string]any)
	switch {
	case on:
		raw["tierJudge"] = map[string]any{"endpointRef": id}
	case cur != nil && cur["endpointRef"] == id:
		delete(raw, "tierJudge")
	}
}
