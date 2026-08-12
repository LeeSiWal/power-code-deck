import { useState } from 'react';
import type { ActivityTodo } from '../../stores/appStore';
import { TodoRows } from './TodoRows';

export function TodoStrip({ todos }: { todos?: ActivityTodo[] }) {
  const [open, setOpen] = useState(false);
  if (!todos?.length) return null;
  const completed = todos.filter((todo) => todo.status === 'completed').length;
  const active = todos.find((todo) => todo.status === 'in_progress');

  return (
    <div className="shrink-0 border-t border-deck-border bg-deck-surface">
      <button
        onClick={() => setOpen((value) => !value)}
        className="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-left"
      >
        <span className="text-deck-accent">☑ {completed}/{todos.length}</span>
        {active && <span className="truncate text-deck-text-dim">▸ {active.activeForm || active.content}</span>}
        <span className="ml-auto text-deck-text-faint">{open ? '∨' : '∧'}</span>
      </button>
      {open && (
        <div className="max-h-44 overflow-y-auto px-3 pb-2 space-y-1">
          <TodoRows todos={todos} />
        </div>
      )}
    </div>
  );
}
