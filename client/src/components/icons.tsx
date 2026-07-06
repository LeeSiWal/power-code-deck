interface IconProps {
  size?: number;
  color?: string;
  className?: string;
}

function I({ size = 16, className, children }: IconProps & { children: React.ReactNode }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" xmlns="http://www.w3.org/2000/svg"
         className={className} style={{ flexShrink: 0 }}>
      {children}
    </svg>
  );
}

export function IconFile({ size, color = '#cccccc', className }: IconProps) {
  return <I size={size} className={className}><path d="M10 1H3.5a.5.5 0 0 0-.5.5v13a.5.5 0 0 0 .5.5h9a.5.5 0 0 0 .5-.5V4L10 1z" stroke={color} strokeWidth="1" fill="none" /><path d="M10 1v3h3" stroke={color} strokeWidth="1" fill="none" /></I>;
}
export function IconFileTs({ size, color = '#3178c6', className }: IconProps) {
  return <I size={size} className={className}><path d="M10 1H3.5a.5.5 0 0 0-.5.5v13a.5.5 0 0 0 .5.5h9a.5.5 0 0 0 .5-.5V4L10 1z" stroke="#555" strokeWidth="1" fill="none" /><path d="M10 1v3h3" stroke="#555" strokeWidth="1" fill="none" /><text x="8" y="12" fontSize="5.5" fontWeight="bold" fill={color} fontFamily="monospace" textAnchor="middle">TS</text></I>;
}
export function IconFileJs({ size, color = '#f1e05a', className }: IconProps) {
  return <I size={size} className={className}><path d="M10 1H3.5a.5.5 0 0 0-.5.5v13a.5.5 0 0 0 .5.5h9a.5.5 0 0 0 .5-.5V4L10 1z" stroke="#555" strokeWidth="1" fill="none" /><path d="M10 1v3h3" stroke="#555" strokeWidth="1" fill="none" /><text x="8" y="12" fontSize="5.5" fontWeight="bold" fill={color} fontFamily="monospace" textAnchor="middle">JS</text></I>;
}

function mkFileIcon(label: string, color: string, stroke = '#555') {
  return function FileIconGenerated({ size, className }: IconProps) {
    const fs = label.length > 2 ? '4.2' : '5.5';
    return (
      <I size={size} className={className}>
        <path d="M10 1H3.5a.5.5 0 0 0-.5.5v13a.5.5 0 0 0 .5.5h9a.5.5 0 0 0 .5-.5V4L10 1z" stroke={stroke} strokeWidth="1" fill="none" />
        <path d="M10 1v3h3" stroke={stroke} strokeWidth="1" fill="none" />
        <text x="8" y="12" fontSize={fs} fontWeight="bold" fill={color} fontFamily="monospace" textAnchor="middle">{label}</text>
      </I>
    );
  };
}

// Image file icon
export function IconFileImage({ size, className }: IconProps) {
  return <I size={size} className={className}><path d="M10 1H3.5a.5.5 0 0 0-.5.5v13a.5.5 0 0 0 .5.5h9a.5.5 0 0 0 .5-.5V4L10 1z" stroke="#555" strokeWidth="1" fill="none" /><path d="M10 1v3h3" stroke="#555" strokeWidth="1" fill="none" /><path d="M3.5 12l2.5-3 1.5 2 1.5-1.5 2 2.5" stroke="#a78bfa" strokeWidth="0.9" fill="none" /><circle cx="5.5" cy="8" r="1" fill="#a78bfa" /></I>;
}
export function IconFolder({ size, color = '#dcb67a', className }: IconProps) {
  return <I size={size} className={className}><path d="M1.5 3a.5.5 0 0 1 .5-.5h4l1.5 1.5h6a.5.5 0 0 1 .5.5v9a.5.5 0 0 1-.5.5h-11a.5.5 0 0 1-.5-.5V3z" fill={color} opacity="0.85" /></I>;
}
export function IconFolderOpen({ size, color = '#dcb67a', className }: IconProps) {
  return <I size={size} className={className}><path d="M1.5 3a.5.5 0 0 1 .5-.5h4l1.5 1.5h6a.5.5 0 0 1 .5.5v1H3.5L1 13V3z" fill={color} opacity="0.7" /><path d="M1 13l2.5-7h11l-2.5 7H1z" fill={color} opacity="0.9" /></I>;
}
export function IconRefresh({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M13 3a7 7 0 0 0-10.95 2M3 13a7 7 0 0 0 10.95-2" stroke={color} strokeWidth="1.2" fill="none" /><path d="M3 1v4h4M13 15v-4h-4" stroke={color} strokeWidth="1.2" fill="none" /></I>;
}
export function IconNewFolder({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M1.5 3a.5.5 0 0 1 .5-.5h4l1.5 1.5h6a.5.5 0 0 1 .5.5v9a.5.5 0 0 1-.5.5h-11a.5.5 0 0 1-.5-.5V3z" stroke={color} strokeWidth="1" fill="none" /><path d="M8 7v5M5.5 9.5h5" stroke={color} strokeWidth="1.2" /></I>;
}
export function IconFilePlus({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M9 1H3.5a.5.5 0 0 0-.5.5v11a.5.5 0 0 0 .5.5h4" stroke={color} strokeWidth="1" fill="none" /><path d="M9 1v3h3" stroke={color} strokeWidth="1" fill="none" /><path d="M11.5 9.5v4M9.5 11.5h4" stroke={color} strokeWidth="1.2" /></I>;
}
export function IconEdit({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M11 2.5l2.5 2.5L6 12.5 3 13l.5-3L11 2.5z" stroke={color} strokeWidth="1" fill="none" /><path d="M9.5 4l2.5 2.5" stroke={color} strokeWidth="1" /></I>;
}
export function IconPlay({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M5 3l8 5-8 5V3z" fill={color} /></I>;
}
export function IconClose({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M4 4l8 8M12 4l-8 8" stroke={color} strokeWidth="1.3" /></I>;
}
export function IconFiles({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M9 1H4a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V5L9 1z" stroke={color} strokeWidth="1" fill="none" /><path d="M9 1v4h4" stroke={color} strokeWidth="1" fill="none" /></I>;
}
export function IconTrash({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M5.5 2h5M3 4h10M4.5 4v9a1 1 0 0 0 1 1h5a1 1 0 0 0 1-1V4" stroke={color} strokeWidth="1" fill="none" /><path d="M6.5 6.5v4M9.5 6.5v4" stroke={color} strokeWidth="1" /></I>;
}
export function IconChevronRight({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M6 3l5 5-5 5" stroke={color} strokeWidth="1.3" fill="none" /></I>;
}
export function IconChevronDown({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M3 6l5 5 5-5" stroke={color} strokeWidth="1.3" fill="none" /></I>;
}
export function IconBack({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M10 3L5 8l5 5" stroke={color} strokeWidth="1.3" fill="none" /></I>;
}
export function IconSearch({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><circle cx="7" cy="7" r="4.5" stroke={color} strokeWidth="1.2" fill="none" /><path d="M10.5 10.5l3.5 3.5" stroke={color} strokeWidth="1.3" /></I>;
}
export function IconTerminal({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M2 4l4 4-4 4" stroke={color} strokeWidth="1.3" fill="none" /><path d="M8 12h6" stroke={color} strokeWidth="1.3" /></I>;
}
export function IconLogout({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M10 3l4 5-4 5" stroke={color} strokeWidth="1.2" fill="none" /><path d="M14 8H5" stroke={color} strokeWidth="1.2" /><path d="M7 2H3a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h4" stroke={color} strokeWidth="1.2" fill="none" /></I>;
}
export function IconPlus({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M8 3v10M3 8h10" stroke={color} strokeWidth="1.3" /></I>;
}
export function IconSettings({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><circle cx="8" cy="8" r="2.5" stroke={color} strokeWidth="1" fill="none" /><path d="M8 1v2M8 13v2M1 8h2M13 8h2M3.05 3.05l1.41 1.41M11.54 11.54l1.41 1.41M3.05 12.95l1.41-1.41M11.54 4.46l1.41-1.41" stroke={color} strokeWidth="1" /></I>;
}
export function IconHome({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M2 8l6-5.5L14 8" stroke={color} strokeWidth="1.2" fill="none" /><path d="M4 7v6.5h3V10h2v3.5h3V7" stroke={color} strokeWidth="1.2" fill="none" /></I>;
}
export function IconLog({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M3 4h10M3 8h7M3 12h10" stroke={color} strokeWidth="1.2" /></I>;
}
export function IconRocket({ size, color = 'currentColor', className }: IconProps) {
  return <I size={size} className={className}><path d="M8 1c3 2 5 6 5 10l-2.5-1.5L8 12l-2.5-2.5L3 11c0-4 2-8 5-10z" stroke={color} strokeWidth="1" fill="none" /><circle cx="8" cy="6" r="1.5" stroke={color} strokeWidth="0.8" fill="none" /></I>;
}

// Agent preset icons
export function IconClaude({ size = 16, className }: IconProps) {
  return <I size={size} className={className}><rect x="2" y="2" width="12" height="12" rx="3" fill="#D97706" opacity="0.15" /><path d="M5 6.5C5 5.67 5.67 5 6.5 5h3c.83 0 1.5.67 1.5 1.5S10.33 8 9.5 8h-3C5.67 8 5 7.33 5 6.5z" fill="#D97706" /><circle cx="6.5" cy="10.5" r="1" fill="#D97706" /><circle cx="9.5" cy="10.5" r="1" fill="#D97706" /></I>;
}
export function IconGemini({ size = 16, className }: IconProps) {
  return <I size={size} className={className}><rect x="2" y="2" width="12" height="12" rx="3" fill="#2563EB" opacity="0.15" /><path d="M8 3L5 8l3 5 3-5-3-5z" fill="#2563EB" opacity="0.8" /></I>;
}
export function IconCodex({ size = 16, className }: IconProps) {
  return <I size={size} className={className}><rect x="2" y="2" width="12" height="12" rx="3" fill="#16A34A" opacity="0.15" /><circle cx="8" cy="8" r="3.5" stroke="#16A34A" strokeWidth="1.5" fill="none" /><circle cx="8" cy="8" r="1" fill="#16A34A" /></I>;
}
export function IconCustom({ size = 16, className }: IconProps) {
  return <I size={size} className={className}><rect x="2" y="2" width="12" height="12" rx="3" fill="#9333EA" opacity="0.15" /><path d="M5 8l3-4 3 4-3 4-3-4z" stroke="#9333EA" strokeWidth="1" fill="none" /></I>;
}

// Sub-agent icons
export function IconRead({ size, color = '#3B82F6', className }: IconProps) {
  return <I size={size} className={className}><path d="M2 3h5l1 1h0l1-1h5v10H9l-1 1-1-1H2V3z" stroke={color} strokeWidth="1" fill="none" /><path d="M8 4v10" stroke={color} strokeWidth="0.8" /></I>;
}
export function IconWrite({ size, color = '#F59E0B', className }: IconProps) {
  return <I size={size} className={className}><path d="M11.5 1.5l3 3-9 9H2.5v-3l9-9z" stroke={color} strokeWidth="1" fill="none" /></I>;
}
export function IconBash({ size, color = '#10B981', className }: IconProps) {
  return <I size={size} className={className}><rect x="1" y="2" width="14" height="12" rx="1.5" stroke={color} strokeWidth="1" fill="none" /><path d="M4 7l2.5 2L4 11" stroke={color} strokeWidth="1.2" fill="none" /><path d="M8 11h4" stroke={color} strokeWidth="1.2" /></I>;
}
export function IconSearchAgent({ size, color = '#8B5CF6', className }: IconProps) {
  return <I size={size} className={className}><circle cx="7" cy="7" r="4" stroke={color} strokeWidth="1.2" fill="none" /><path d="M10 10l4 4" stroke={color} strokeWidth="1.3" /></I>;
}
export function IconThink({ size, color = '#EC4899', className }: IconProps) {
  return <I size={size} className={className}><path d="M4 10c-1-1-1.5-2.5-1-4s2-3 4-3 3.5 1 4 3-.5 3-1 4" stroke={color} strokeWidth="1" fill="none" /><path d="M6 12h4M6.5 14h3" stroke={color} strokeWidth="1" /></I>;
}
export function IconGear({ size, color = '#6B7280', className }: IconProps) {
  return <I size={size} className={className}><circle cx="8" cy="8" r="2" stroke={color} strokeWidth="1" fill="none" /><path d="M8 1v2M8 13v2M1 8h2M13 8h2M3 3l1.5 1.5M11.5 11.5L13 13M3 13l1.5-1.5M11.5 4.5L13 3" stroke={color} strokeWidth="0.8" /></I>;
}

export const FILE_ICON_MAP: Record<string, React.ComponentType<IconProps>> = {
  // TypeScript
  ts: IconFileTs, tsx: IconFileTs,
  // JavaScript
  js: IconFileJs, jsx: IconFileJs, mjs: mkFileIcon('JS', '#f1e05a'), cjs: mkFileIcon('JS', '#f1e05a'),
  // Python
  py: mkFileIcon('PY', '#3572A5'), pyw: mkFileIcon('PY', '#3572A5'),
  // Go
  go: mkFileIcon('GO', '#00ADD8'),
  // Rust
  rs: mkFileIcon('RS', '#dea584'),
  // Java / JVM
  java: mkFileIcon('JV', '#b07219'), kt: mkFileIcon('KT', '#A97BFF'), scala: mkFileIcon('SC', '#c22d40'),
  // C / C++
  c: mkFileIcon('C', '#9999ff'), h: mkFileIcon('C', '#9999ff'), cpp: mkFileIcon('C+', '#f34b7d'), cc: mkFileIcon('C+', '#f34b7d'), hpp: mkFileIcon('C+', '#f34b7d'),
  // Web
  html: mkFileIcon('HT', '#e34c26'), htm: mkFileIcon('HT', '#e34c26'),
  css: mkFileIcon('CS', '#563d7c'), scss: mkFileIcon('SC', '#c6538c'), sass: mkFileIcon('SC', '#c6538c'), less: mkFileIcon('LS', '#1d365d'),
  vue: mkFileIcon('VU', '#42b883'), svelte: mkFileIcon('SV', '#ff3e00'),
  // Config / Data
  json: mkFileIcon('{}', '#cbcb41'), jsonc: mkFileIcon('{}', '#cbcb41'),
  yaml: mkFileIcon('YL', '#cb171e'), yml: mkFileIcon('YL', '#cb171e'),
  toml: mkFileIcon('TM', '#9c4221'), ini: mkFileIcon('IN', '#6b7280'), cfg: mkFileIcon('CF', '#6b7280'),
  xml: mkFileIcon('XL', '#0060ac'), csv: mkFileIcon('CV', '#237346'),
  // Markdown / Docs
  md: mkFileIcon('MD', '#519aba'), mdx: mkFileIcon('MD', '#519aba'), rst: mkFileIcon('RS', '#879a6e'), txt: mkFileIcon('TX', '#8b949e'),
  // Shell
  sh: mkFileIcon('SH', '#89e051'), bash: mkFileIcon('SH', '#89e051'), zsh: mkFileIcon('SH', '#89e051'), fish: mkFileIcon('SH', '#89e051'),
  // Env / Git / Docker
  env: mkFileIcon('ENV', '#eacc3a', '#444'), gitignore: mkFileIcon('GIT', '#f05033'), gitattributes: mkFileIcon('GIT', '#f05033'), dockerignore: mkFileIcon('DK', '#0db7ed'),
  dockerfile: mkFileIcon('DK', '#0db7ed'),
  // Database / Query
  sql: mkFileIcon('SQ', '#e38c00'), graphql: mkFileIcon('GQ', '#e10098'), gql: mkFileIcon('GQ', '#e10098'),
  // Lock / Log
  lock: mkFileIcon('LK', '#6b7280'), log: mkFileIcon('LG', '#6b7280'),
  // Ruby / PHP / Swift
  rb: mkFileIcon('RB', '#701516'), php: mkFileIcon('PH', '#777bb4'), swift: mkFileIcon('SW', '#F05138'),
  // Images
  png: IconFileImage, jpg: IconFileImage, jpeg: IconFileImage, gif: IconFileImage, webp: IconFileImage, svg: IconFileImage, ico: IconFileImage, bmp: IconFileImage,
};

export const AGENT_ICON_MAP: Record<string, React.ComponentType<IconProps>> = {
  'claude-code': IconClaude, 'gemini-cli': IconGemini, 'codex-cli': IconCodex, custom: IconCustom,
};

export const SUB_AGENT_ICON_MAP: Record<string, React.ComponentType<IconProps>> = {
  read: IconRead, write: IconWrite, bash: IconBash, search: IconSearchAgent,
  think: IconThink, unknown: IconGear,
};

export type { IconProps };
