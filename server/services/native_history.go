package services

import (
	"encoding/json"
	"fmt"
)

// NativeHistoryStore is wired at startup; the store owns transactions and limits.
type NativeHistoryStore interface {
	Append(agent, provider string, raw json.RawMessage, resume string) error
	Load(agent, provider string) ([]json.RawMessage, error)
}

func (s *NativeService) SetHistoryStore(store NativeHistoryStore) { s.historyStore = store }

// restoreNativeHistory runs before the session is published or starts pumping.
// An unfinished saved turn is closed explicitly; replay must not imply that a
// process survived a server restart. The marker is persisted once, not per open.
func (s *NativeService) restoreNativeHistory(sess *nativeSession) error {
	if sess.kind != "antigravity" || s.historyStore == nil {
		return nil
	}
	raws, err := s.historyStore.Load(sess.id, sess.kind)
	if err != nil {
		return fmt.Errorf("load conversation history: %w", err)
	}
	active := false
	for _, raw := range raws {
		ev, err := ParseStreamEvent(raw)
		if err != nil {
			return fmt.Errorf("decode conversation history: %w", err)
		}
		sess.history = append(sess.history, ev)
		if ev.Type == "assistant" || ev.Type == "stream_event" || ev.Type == "system" && ev.Subtype == "init" {
			active = true
		}
		if ev.Type == "user" && ev.Message != nil {
			for _, b := range ev.Message.Content {
				if b.Type == "text" {
					active = true
				}
			}
		}
		if ev.Type == "result" {
			active = false
		}
	}
	if active {
		raw := json.RawMessage(`{"type":"result","is_error":true,"provider_notice":true,"terminal_reason":"session_ended","result":"이전 연결이 종료되어 작업 완료 여부를 확인할 수 없습니다. 결과를 확인한 뒤 이어서 요청해주세요."}`)
		if err := s.historyStore.Append(sess.id, sess.kind, raw, ""); err != nil {
			return fmt.Errorf("save interrupted history: %w", err)
		}
		ev, _ := ParseStreamEvent(raw)
		sess.history = append(sess.history, ev)
		if len(sess.history) > maxNativeHistory {
			sess.history = sess.history[len(sess.history)-maxNativeHistory:]
		}
	}
	return nil
}
