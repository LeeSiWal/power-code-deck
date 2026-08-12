import type { ActivityTodo } from '../../stores/appStore';
import { TodoRows } from './TodoRows';

/**
 * 우측 패널 맨 위에 고정되는 할일 구역. 항상 펼쳐져 있다 — 이 기능의 목적이
 * "찾아보지 않아도 남은 양을 아는 것"이라, 열어야 보이면 목적이 무너진다.
 *
 * 높이는 부모가 정한다(사용자가 드래그해 조절하고 localStorage에 기억한다).
 * 할일이 없으면 아무것도 그리지 않는다 — 빈 껍데기는 공간만 먹고 알려주는 게 없다.
 */
export function TodoPanel({ todos, height }: { todos?: ActivityTodo[]; height: number }) {
  if (!todos?.length) return null;
  const completed = todos.filter((todo) => todo.status === 'completed').length;
  const active = todos.find((todo) => todo.status === 'in_progress');

  return (
    <div
      className="shrink-0 flex flex-col overflow-hidden border-b border-deck-border bg-deck-surface"
      style={{ height: `${height}px` }}
    >
      <div className="shrink-0 flex items-center gap-2 px-3 py-1.5 text-xs">
        <span className="text-deck-accent">☑ {completed}/{todos.length}</span>
        {active && (
          <span className="truncate text-deck-text-dim">▸ {active.activeForm || active.content}</span>
        )}
      </div>
      <div className="flex-1 min-h-0 overflow-y-auto px-3 pb-2 space-y-1">
        <TodoRows todos={todos} />
      </div>
    </div>
  );
}
