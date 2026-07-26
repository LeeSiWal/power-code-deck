import { Link, useLocation } from 'react-router-dom';
import { IconBell, IconLog, IconSettings, IconDevices } from '../icons';
import { NotificationBadge } from '../notification/NotificationBadge';

// 통합 후 /dashboard와 /control은 같은 화면이므로 탭도 하나로 합쳤다.
// 프로젝트 추가는 관제실 헤더 버튼이 담당한다(탭이 아니라 행동이라서).
const NAV_ITEMS = [
  { href: '/control', label: 'Deck', Icon: IconDevices },
  { href: '/notifications', label: 'Alerts', Icon: IconBell },
  { href: '/logs', label: 'Logs', Icon: IconLog },
  { href: '/settings', label: 'Settings', Icon: IconSettings },
];

// 배지는 알림 탭에 붙는다. 예전에는 Deck 탭에 있었는데, 그때는 알림을 볼 화면이
// 없어서 배지가 "어딘가에 알림이 있다"는 신호일 뿐이었다. 이제 누를 곳이 생겼으니
// 배지는 그 목적지에 있어야 한다.
const BADGE_HREF = '/notifications';

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
            className="flex flex-col items-center gap-1 py-3 px-4 text-xs min-w-[52px]"
            style={{ color: active ? '#6366f1' : '#8791a4' }}
          >
            <div className="relative">
              <item.Icon size={22} />
              {item.href === BADGE_HREF && (
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
