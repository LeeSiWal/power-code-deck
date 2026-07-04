import { Link, useLocation } from 'react-router-dom';
import { IconHome, IconLog, IconSettings, IconLogout } from '../icons';
import { NotificationBadge } from '../notification/NotificationBadge';

const NAV_ITEMS = [
  { href: '/', label: 'Projects', Icon: IconHome },
  { href: '/dashboard', label: 'Dashboard', Icon: IconHome },
  { href: '/logs', label: 'Logs', Icon: IconLog },
  { href: '/settings', label: 'Settings', Icon: IconSettings },
];

interface SidebarProps {
  onLogout: () => void;
}

export function Sidebar({ onLogout }: SidebarProps) {
  const { pathname } = useLocation();

  return (
    <aside className="hidden md:flex flex-col w-48 bg-deck-surface border-r border-deck-border h-full">
      <div className="p-4 border-b border-deck-border flex items-center justify-between">
        <span className="text-sm font-bold text-deck-text">PowerCodeDeck</span>
        <NotificationBadge />
      </div>

      <nav className="flex-1 py-2">
        {NAV_ITEMS.map((item) => {
          const active = pathname === item.href;
          return (
            <Link
              key={item.href}
              to={item.href}
              className={`flex items-center gap-2 px-4 py-2 text-sm transition-colors ${
                active ? 'bg-deck-accent/10 text-deck-accent' : 'text-deck-text-dim hover:bg-deck-border/30'
              }`}
            >
              <item.Icon size={16} />
              {item.label}
            </Link>
          );
        })}
      </nav>

      <button
        onClick={onLogout}
        className="flex items-center gap-2 px-4 py-3 text-sm text-deck-text-dim hover:bg-deck-border/30 border-t border-deck-border"
      >
        <IconLogout size={16} />
        Logout
      </button>
    </aside>
  );
}
