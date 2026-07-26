import { Link, useLocation } from 'react-router-dom';
import { IconLog, IconSettings, IconDevices } from '../icons';
import { NotificationBadge } from '../notification/NotificationBadge';

// 통합 후 /dashboard와 /control은 같은 화면이므로 탭도 하나로 합쳤다.
// 프로젝트 추가는 관제실 헤더 버튼이 담당한다(탭이 아니라 행동이라서).
const NAV_ITEMS = [
  { href: '/control', label: 'Deck', Icon: IconDevices },
  { href: '/logs', label: 'Logs', Icon: IconLog },
  { href: '/settings', label: 'Settings', Icon: IconSettings },
];

export function BottomNav() {
  const { pathname } = useLocation();

  return (
    <nav className="md:hidden flex items-center justify-around safe-bottom bg-deck-surface border-t border-deck-border">
      {NAV_ITEMS.map((item) => {
        const active = pathname === item.href;
        return (
          <Link
            key={item.href}
            to={item.href}
            // 탭 전환은 스택을 쌓지 않는다 — 그래야 뒤로 가기가 탭 방문 순서를
            // 거꾸로 걷지 않는다.
            replace
            className="flex flex-col items-center gap-1 py-3 px-5 text-xs min-w-[56px]"
            style={{ color: active ? '#6366f1' : '#8791a4' }}
          >
            <div className="relative">
              <item.Icon size={22} />
              {item.href === '/control' && (
                <NotificationBadge className="absolute -top-1.5 -right-2.5" />
              )}
            </div>
            <span className="text-[10px]">{item.label}</span>
          </Link>
        );
      })}
    </nav>
  );
}
