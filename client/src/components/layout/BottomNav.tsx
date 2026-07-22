import { Link, useLocation } from 'react-router-dom';
import { IconHome, IconLog, IconSettings, IconDevices } from '../icons';
import { NotificationBadge } from '../notification/NotificationBadge';

const NAV_ITEMS = [
  { href: '/dashboard', label: 'Home', Icon: IconHome },
  { href: '/control', label: 'Control', Icon: IconDevices },
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
            className="flex flex-col items-center gap-1 py-3 px-5 text-xs min-w-[56px]"
            style={{ color: active ? '#6366f1' : '#8791a4' }}
          >
            <div className="relative">
              <item.Icon size={22} />
              {item.href === '/dashboard' && (
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
