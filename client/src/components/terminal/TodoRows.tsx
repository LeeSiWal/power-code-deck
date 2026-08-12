import type { ActivityTodo } from '../../stores/appStore';

/**
 * 할일 한 줄씩. 스트립과 사이드 패널이 함께 쓴다.
 *
 * 글리프·색·취소선, 그리고 진행 중일 때 activeForm을 쓰는 규칙이 여기 한 곳에만
 * 있어야 같은 목록이 어느 표면에서든 같게 읽힌다. 두 곳에 복제하면 한쪽만 바뀌는
 * 순간 사용자에게는 서로 다른 목록으로 보인다.
 */
export function TodoRows({ todos }: { todos: ActivityTodo[] }) {
  return (
    <>
      {todos.map((todo, index) => (
        <div key={`${index}-${todo.content}`} className="flex gap-2 text-xs">
          <span className={
            todo.status === 'completed'
              ? 'text-emerald-400'
              : todo.status === 'in_progress'
                ? 'text-deck-accent'
                : 'text-deck-text-faint'
          }>
            {todo.status === 'completed' ? '✓' : todo.status === 'in_progress' ? '▸' : '○'}
          </span>
          <span className={todo.status === 'completed' ? 'text-deck-text-faint line-through' : 'text-deck-text-dim'}>
            {todo.status === 'in_progress' ? todo.activeForm || todo.content : todo.content}
          </span>
        </div>
      ))}
    </>
  );
}
