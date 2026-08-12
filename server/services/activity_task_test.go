package services

import "testing"

// The CLI moved the checklist from TodoWrite (whole-list snapshot) to
// TaskCreate/TaskUpdate (deltas). The parser only knew TodoWrite, so on every
// session that used the new tools the three surfaces — terminal strip, chat card,
// control-room tile — went silently blank. Nothing errored; there was simply never
// any input. That is exactly the risk the design doc listed and mis-scoped: it
// guarded against the input SCHEMA changing, not the tool being RENAMED.
//
// Verified against 437 transcripts before writing this: TaskCreate always carries
// {subject, description, activeForm}; TaskUpdate carries {taskId, status} and may
// also revise subject/description/activeForm; status ∈ in_progress | completed |
// deleted, and is sometimes absent.
func TestTaskCreateBuildsTheChecklist(t *testing.T) {
	w := newTestWatcher()
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:00:00Z","message":{"content":[{"type":"tool_use","id":"tc1","name":"TaskCreate","input":{"subject":"권한 수정","description":"…","activeForm":"권한 수정 중"}}]}}`)
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:00:01Z","message":{"content":[{"type":"tool_use","id":"tc2","name":"TaskCreate","input":{"subject":"테스트 추가","description":"…","activeForm":"테스트 추가 중"}}]}}`)

	if len(w.todos) != 2 {
		t.Fatalf("TaskCreate did not build a checklist: %+v", w.todos)
	}
	if w.todos[0].Content != "권한 수정" || w.todos[0].ActiveForm != "권한 수정 중" {
		t.Fatalf("subject/activeForm not mapped: %+v", w.todos[0])
	}
	// A freshly created task has not started yet — the strip renders it as ○.
	if w.todos[0].Status != "pending" {
		t.Fatalf("new task status = %q, want pending", w.todos[0].Status)
	}
}

// taskId is the 1-based creation order within the transcript. Confirmed across every
// session that used these tools: the TaskCreate count equals the highest taskId ever
// referenced. We derive it by counting rather than parsing "Task #1 created
// successfully" out of the tool_result, because depending on an English prose string
// is the same class of fragility that broke this feature in the first place.
func TestTaskUpdateMovesTheRightTaskByCreationOrder(t *testing.T) {
	w := newTestWatcher()
	for _, s := range []string{"첫째", "둘째", "셋째"} {
		w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:00:00Z","message":{"content":[{"type":"tool_use","id":"c","name":"TaskCreate","input":{"subject":"` + s + `","activeForm":"` + s + ` 중"}}]}}`)
	}
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:01:00Z","message":{"content":[{"type":"tool_use","id":"u1","name":"TaskUpdate","input":{"taskId":"2","status":"in_progress"}}]}}`)
	if w.todos[1].Status != "in_progress" {
		t.Fatalf("taskId 2 should be the SECOND created task: %+v", w.todos)
	}
	if w.todos[0].Status != "pending" || w.todos[2].Status != "pending" {
		t.Fatalf("TaskUpdate touched the wrong rows: %+v", w.todos)
	}

	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:02:00Z","message":{"content":[{"type":"tool_use","id":"u2","name":"TaskUpdate","input":{"taskId":"2","status":"completed"}}]}}`)
	if w.todos[1].Status != "completed" {
		t.Fatalf("completion not applied: %+v", w.todos)
	}
}

// "deleted" is a real status in the wild (3 occurrences). Rendering a deleted task as
// an outstanding item would overstate what is left — the one number the user reads.
func TestTaskUpdateDeletedRemovesTheRow(t *testing.T) {
	w := newTestWatcher()
	for _, s := range []string{"남을 것", "지울 것"} {
		w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:00:00Z","message":{"content":[{"type":"tool_use","id":"c","name":"TaskCreate","input":{"subject":"` + s + `"}}]}}`)
	}
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:01:00Z","message":{"content":[{"type":"tool_use","id":"u","name":"TaskUpdate","input":{"taskId":"2","status":"deleted"}}]}}`)
	if len(w.todos) != 1 || w.todos[0].Content != "남을 것" {
		t.Fatalf("deleted task should leave the list: %+v", w.todos)
	}
	// The surviving task keeps its identity: a later update for id 1 must still land.
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:02:00Z","message":{"content":[{"type":"tool_use","id":"u2","name":"TaskUpdate","input":{"taskId":"1","status":"completed"}}]}}`)
	if w.todos[0].Status != "completed" {
		t.Fatalf("ids must survive a deletion (no re-indexing): %+v", w.todos)
	}
}

// TaskUpdate may revise the wording, and may carry no status at all.
func TestTaskUpdateRevisesWordingAndToleratesMissingStatus(t *testing.T) {
	w := newTestWatcher()
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:00:00Z","message":{"content":[{"type":"tool_use","id":"c","name":"TaskCreate","input":{"subject":"옛 제목","activeForm":"옛 진행형"}}]}}`)
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:00:01Z","message":{"content":[{"type":"tool_use","id":"u1","name":"TaskUpdate","input":{"taskId":"1","status":"in_progress"}}]}}`)
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:00:02Z","message":{"content":[{"type":"tool_use","id":"u2","name":"TaskUpdate","input":{"taskId":"1","subject":"새 제목","activeForm":"새 진행형"}}]}}`)
	if w.todos[0].Content != "새 제목" || w.todos[0].ActiveForm != "새 진행형" {
		t.Fatalf("wording revision not applied: %+v", w.todos[0])
	}
	if w.todos[0].Status != "in_progress" {
		t.Fatalf("an update with no status must not reset it: %+v", w.todos[0])
	}
}

// The two traps the design doc named apply to the new tools exactly as they did to
// TodoWrite: a subagent's checklist must not overwrite the main one, and garbage must
// leave the previous list standing.
func TestTaskToolsIgnoreSidechainAndGarbage(t *testing.T) {
	w := newTestWatcher()
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:00:00Z","message":{"content":[{"type":"tool_use","id":"c","name":"TaskCreate","input":{"subject":"메인"}}]}}`)
	w.processLine(`{"type":"assistant","isSidechain":true,"timestamp":"2026-08-12T10:00:01Z","message":{"content":[{"type":"tool_use","id":"s","name":"TaskCreate","input":{"subject":"서브에이전트"}}]}}`)
	w.processLine(`{"type":"assistant","isSidechain":true,"timestamp":"2026-08-12T10:00:02Z","message":{"content":[{"type":"tool_use","id":"s2","name":"TaskUpdate","input":{"taskId":"1","status":"completed"}}]}}`)
	if len(w.todos) != 1 || w.todos[0].Content != "메인" || w.todos[0].Status != "pending" {
		t.Fatalf("subagent tasks leaked into the main checklist: %+v", w.todos)
	}

	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:00:03Z","message":{"content":[{"type":"tool_use","id":"bad","name":"TaskCreate","input":{"subject":""}}]}}`)
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:00:04Z","message":{"content":[{"type":"tool_use","id":"bad2","name":"TaskUpdate","input":{"taskId":"99","status":"completed"}}]}}`)
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:00:05Z","message":{"content":[{"type":"tool_use","id":"bad3","name":"TaskUpdate","input":{"taskId":"nope","status":"completed"}}]}}`)
	if len(w.todos) != 1 || w.todos[0].Content != "메인" || w.todos[0].Status != "pending" {
		t.Fatalf("garbage input disturbed the checklist: %+v", w.todos)
	}
}

// TodoWrite still appears in the wild on the main chain, so the old path must keep
// working — this is an addition, not a replacement. A TodoWrite snapshot replaces
// whatever the task tools had built, because it is by definition the whole list.
func TestTodoWriteStillWorksAlongsideTaskTools(t *testing.T) {
	w := newTestWatcher()
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:00:00Z","message":{"content":[{"type":"tool_use","id":"c","name":"TaskCreate","input":{"subject":"task-tool 항목"}}]}}`)
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:00:01Z","message":{"content":[{"type":"tool_use","id":"tw","name":"TodoWrite","input":{"todos":[{"content":"스냅샷 항목","status":"in_progress","activeForm":"하는 중"}]}}]}}`)
	if len(w.todos) != 1 || w.todos[0].Content != "스냅샷 항목" {
		t.Fatalf("TodoWrite snapshot should replace the list: %+v", w.todos)
	}
	// And the task tools must keep working after it, numbering from the new list.
	w.processLine(`{"type":"assistant","isSidechain":false,"timestamp":"2026-08-12T10:00:02Z","message":{"content":[{"type":"tool_use","id":"u","name":"TaskUpdate","input":{"taskId":"1","status":"completed"}}]}}`)
	if w.todos[0].Status != "completed" {
		t.Fatalf("TaskUpdate should address the snapshot's rows: %+v", w.todos)
	}
}
