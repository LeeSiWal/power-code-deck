package services

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// fakeNativeDriver stands in for a running CLI so a mode switch can be observed
// without spawning one. It records every in-place mode change and every Stop, which
// is exactly the difference the tests below care about: Stop means the process was
// killed, modes means it was steered.
type fakeNativeDriver struct {
	events  chan *StreamEvent
	modes   []string
	modeErr error
	stopped int
}

func newFakeNativeDriver() *fakeNativeDriver {
	return &fakeNativeDriver{events: make(chan *StreamEvent)}
}

func (f *fakeNativeDriver) Start() error                { return nil }
func (f *fakeNativeDriver) Events() <-chan *StreamEvent { return f.events }
func (f *fakeNativeDriver) Send(string) error           { return nil }
func (f *fakeNativeDriver) Interrupt() error            { return nil }
func (f *fakeNativeDriver) ConversationID() string      { return "conv-1" }
func (f *fakeNativeDriver) Stop()                       { f.stopped++ }
func (f *fakeNativeDriver) SetPermissionMode(m string) error {
	if f.modeErr != nil {
		return f.modeErr
	}
	f.modes = append(f.modes, m)
	return nil
}

// 전체 허용(bypassPermissions)과 자동(auto)은 둘 다 서버가 소유하는 정책이다. CLI에는
// 넘기지 않고 기본 모드로 띄운 뒤 승인 브리지에서 결정한다.
//
// bypass를 CLI 플래그로 넘기면 드라이버가 승인 브리지를 통째로 떼야 했고(그래야 CLI가
// 거부하지 않는다), 그 순간 "물어볼 채널이 없어서 조용히 거부"되는 구멍이 생긴다.
// 그리고 CLI는 런타임 set_permission_mode로 bypass 전환을 거부하므로, 그 모드에 들어가고
// 나오는 것만으로도 프로세스 재시작이 강제된다. 둘 다 CLI에 bypass를 넘기지 않으면 사라진다.
func TestCLIPermissionModeKeepsServerOwnedModesOffTheCLI(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{BypassMode, ""},
		{AutoMode, ""},
		{PlanMode, PlanMode},
		{"acceptEdits", "acceptEdits"},
		{"", ""},
	} {
		if got := cliPermissionMode(tc.mode); got != tc.want {
			t.Errorf("cliPermissionMode(%q) = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

// 승인 브리지는 어떤 모드에서도 붙어 있어야 한다. 브리지가 없으면 CLI가 승인을 물어야 할
// 때 물어볼 상대가 없어 그대로 거부하고, 턴은 성공으로 끝난다 — 사용자에게는 "승인 요청
// 없이 작업이 스킵된" 것으로 보인다.
func TestBuildArgsKeepsApprovalBridgeInEveryMode(t *testing.T) {
	for _, mode := range []string{"", "acceptEdits", PlanMode, BypassMode} {
		d := NewClaudeDriver(ClaudeConfig{SessionID: "a1", PermissionMode: mode, SelfPath: "/opt/pcd"})
		args := strings.Join(d.buildArgs(), " ")
		if !strings.Contains(args, "--permission-prompt-tool mcp__pcd__approve") {
			t.Errorf("mode %q: approval bridge missing from args: %s", mode, args)
		}
		if !strings.Contains(args, "--mcp-config") {
			t.Errorf("mode %q: --mcp-config missing from args: %s", mode, args)
		}
	}
}

// --effort는 항상 붙어야 한다. 빼면 "effort 없음"이 아니라 CLI 기본값(xhigh)이 되고,
// 라우팅 같은 가벼운 턴까지 최상위 설정으로 돌아 토큰이 필요 이상으로 나간다. 그리고
// 클라이언트가 이상한 값을 보내도 그게 커맨드라인까지 가면 세션 시작 자체가 실패하므로,
// 알 수 없는 값은 기본값으로 접힌다.
func TestBuildArgsAlwaysPinsEffort(t *testing.T) {
	cases := []struct{ given, want string }{
		{"", DefaultEffort},       // 고른 적 없음 → 덱의 기본값
		{"low", "low"},            // 유효한 값은 그대로
		{"max", "max"},            //
		{"xhigh", "xhigh"},        //
		{"ultra", DefaultEffort},  // 오타/구버전 값 → 기본값으로 방어
		{"HIGH", DefaultEffort},   // CLI는 소문자만 받는다
		{"--flag", DefaultEffort}, // 인자 주입 시도도 값으로만 취급된다
	}
	for _, tc := range cases {
		d := NewClaudeDriver(ClaudeConfig{SessionID: "a1", Effort: tc.given, SelfPath: "/opt/pcd"})
		args := d.buildArgs()
		idx := -1
		for i, a := range args {
			if a == "--effort" {
				idx = i
				break
			}
		}
		if idx < 0 || idx+1 >= len(args) {
			t.Fatalf("effort %q: --effort missing from args: %v", tc.given, args)
		}
		if got := args[idx+1]; got != tc.want {
			t.Errorf("effort %q: --effort %q, want %q", tc.given, got, tc.want)
		}
	}
}

// 모드를 바꿔도 진행 중인 작업이 죽으면 안 된다. 예전 SetMode는 매번 restart()를 통해
// 프로세스를 kill했고, 그래서 작업 도중 모드를 바꾸면 턴이 통째로 사라졌다.
func TestSetModeSwitchesInPlaceWithoutKillingTheProcess(t *testing.T) {
	s := NewNativeService("http://127.0.0.1:0")
	saved := make(chan [4]string, 8)
	s.SetConfigPersistence(func(id, model, mode, effort string) { saved <- [4]string{id, model, mode, effort} }, nil)

	fd := newFakeNativeDriver()
	s.mu.Lock()
	s.sessions["a1"] = &nativeSession{id: "a1", driver: fd, kind: "claude", cwd: "/tmp/proj", model: "claude-opus-5", effort: "high"}
	s.policies["a1"] = sessionPolicy{mode: "", cwd: "/tmp/proj"}
	s.mu.Unlock()

	if err := s.SetMode("a1", PlanMode); err != nil {
		t.Fatalf("SetMode(plan): %v", err)
	}
	if fd.stopped != 0 {
		t.Fatalf("SetMode killed the process %d time(s) — in-flight work would be lost", fd.stopped)
	}
	if len(fd.modes) != 1 || fd.modes[0] != PlanMode {
		t.Fatalf("driver modes = %v, want [plan]", fd.modes)
	}
	model, mode, effort := s.Config("a1")
	if model != "claude-opus-5" || mode != PlanMode {
		t.Fatalf("Config = (%q, %q), want (claude-opus-5, plan)", model, mode)
	}
	// An in-place mode switch must not disturb the effort the session launched with.
	if effort != "high" {
		t.Fatalf("Config effort = %q, want high — SetMode must not reset it", effort)
	}
	s.mu.RLock()
	pol := s.policies["a1"]
	s.mu.RUnlock()
	if pol.mode != PlanMode {
		t.Fatalf("policy mode = %q, want plan — the approve bridge would judge by the old mode", pol.mode)
	}
	select {
	case got := <-saved:
		if got != [4]string{"a1", "claude-opus-5", PlanMode, "high"} {
			t.Fatalf("persisted %v, want [a1 claude-opus-5 plan high]", got)
		}
	default:
		t.Fatal("mode change was not persisted — another device would resume with the old mode")
	}

	// 전체 허용으로 바꿔도 CLI에는 기본 모드가 간다. 서버 정책만 bypass가 된다.
	if err := s.SetMode("a1", BypassMode); err != nil {
		t.Fatalf("SetMode(bypass): %v", err)
	}
	if fd.stopped != 0 {
		t.Fatalf("switching to 전체 허용 killed the process")
	}
	if len(fd.modes) != 2 || fd.modes[1] != "" {
		t.Fatalf("driver modes = %v, want the CLI to be told default (\"\") for 전체 허용", fd.modes)
	}
	s.mu.RLock()
	pol = s.policies["a1"]
	s.mu.RUnlock()
	if pol.mode != BypassMode {
		t.Fatalf("policy mode = %q, want bypassPermissions", pol.mode)
	}
}

// 드라이버가 제자리 전환을 못 하면(예전 CLI 빌드, Codex) 예전처럼 재시작으로 물러난다.
// 여기서는 cwd가 없어 재시작이 실패하므로, 되돌아간 사실이 Stop 호출과 에러로 드러난다.
func TestSetModeFallsBackToRestartWhenTheDriverCannotSwitch(t *testing.T) {
	s := NewNativeService("http://127.0.0.1:0")
	fd := newFakeNativeDriver()
	fd.modeErr = errors.New("this CLI build cannot switch modes")

	s.mu.Lock()
	s.sessions["a1"] = &nativeSession{id: "a1", driver: fd, kind: "claude", cwd: "/nonexistent-pcd-mode-test"}
	s.policies["a1"] = sessionPolicy{cwd: "/nonexistent-pcd-mode-test"}
	s.mu.Unlock()

	if err := s.SetMode("a1", PlanMode); err == nil {
		t.Fatal("a failed restart must surface, not report success")
	}
	if fd.stopped == 0 {
		t.Fatal("driver refused the in-place switch but SetMode never fell back to a restart")
	}
}

// SetPermissionMode는 CLI의 control_response를 기다린다. 응답을 안 보고 성공으로 치면,
// CLI가 거부해도(예: bypass 런타임 전환) 서버는 바뀐 줄 알고 정책만 어긋난다.
func TestSetPermissionModeWaitsForTheCLIsAnswer(t *testing.T) {
	for _, tc := range []struct {
		name    string
		subtype string
		errMsg  string
		wantErr string
	}{
		{name: "success", subtype: "success"},
		{name: "refusal", subtype: "error", errMsg: "not launched with --dangerously-skip-permissions", wantErr: "dangerously"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewClaudeDriver(ClaudeConfig{SessionID: "a1"})
			outR, outW := io.Pipe() // CLI stdout → readPump
			inR, inW := io.Pipe()   // driver stdin → the fake CLI
			d.stdin = inW

			go d.readPump(outR)
			go func() { //nolint:staticcheck — drain so readPump never blocks
				for range d.Events() {
				}
			}()
			// The fake CLI: answer every control_request with the scripted response.
			go func() {
				sc := bufio.NewScanner(inR)
				for sc.Scan() {
					var req ControlRequest
					if json.Unmarshal(sc.Bytes(), &req) != nil || req.RequestID == "" {
						continue
					}
					body := map[string]any{"subtype": tc.subtype, "request_id": req.RequestID}
					if tc.errMsg != "" {
						body["error"] = tc.errMsg
					}
					line, _ := json.Marshal(map[string]any{"type": "control_response", "response": body})
					outW.Write(append(line, '\n'))
				}
			}()
			defer outW.Close()

			err := d.SetPermissionMode(PlanMode)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("SetPermissionMode: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("SetPermissionMode error = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
}
