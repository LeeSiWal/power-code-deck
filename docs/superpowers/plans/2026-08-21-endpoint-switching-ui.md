# 엔드포인트 전환 UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 사용자가 화면에서 원격 워크스테이션을 등록하고 오갈 수 있게 한다. 지금은 `localStorage`를 직접 편집해야만 가능하다.

**Architecture:** 배관은 이미 다 있다 — `client/src/lib/endpoint.ts`가 엔드포인트 목록·현재 선택·엔드포인트별 토큰의 단일 출처이고, `api.ts`/`ws.ts`가 그 위에서만 URL을 조립한다(2026-08-21 엔드포인트 추상화). 없는 것은 **화면**뿐이다.

이 계획은 서버를 건드리지 않는다. `endpoint.ts`에 목록 편집 헬퍼를 더하고, 설정 화면에 카드를 하나 붙이고, **전환은 페이지 리로드로 처리한다.** 리로드가 타협처럼 보이지만 실제로는 정확한 선택이다: 부팅 시 `App.tsx`가 엔드포인트별로 익명 토큰을 새로 발급받고(`App.tsx:56-75`), WS는 토큰+엔드포인트가 바뀌면 소켓을 새로 열며, 스토어의 에이전트·트레이스·승인 큐가 전부 특정 서버의 것이다. 이걸 부분 갱신으로 흉내 내면 "옛 서버의 잔상이 새 서버 화면에 남는" 버그가 반드시 생긴다.

**Tech Stack:** React 18 + TypeScript, Zustand, Tailwind. 새 npm 패키지 금지.

## Global Constraints

- 스펙: `docs/superpowers/specs/2026-08-21-desktop-and-remote-design.md` §1·목표("여러 워크스페이스 호스트를 하나의 클라이언트에서 오간다").
- **서버 변경 0.** 이 계획은 클라이언트만 만진다.
- **루프백 단독 사용자에게는 아무것도 달라지지 않아야 한다.** 엔드포인트가 하나(`local`)뿐이면 카드는 목록 하나와 "추가" 버튼만 보인다.
- 새 npm 패키지 금지. 새 상태 관리 라이브러리 금지.
- **이 리포에는 클라이언트 테스트 러너가 없다.** `package.json`에 `test` 스크립트도 vitest도 없다. 유일한 선례인 `client/src/lib/endpoint.test.ts`는 프레임워크 없이 모듈 스코프에서 단언하고 실패 시 throw하는 파일이고, `tsconfig.json`의 `include: ["src"]`에 걸려 **`tsc --noEmit`으로 타입만 검사된다(실행되지 않는다).** 같은 관례를 따르되, 진짜 검증은 Task 5의 수동 확인이다. 러너를 새로 도입하지 말 것.
- 클라이언트 검증: `cd client && ./node_modules/.bin/tsc --noEmit`
- 마지막에 `dist/pcd.exe` 재빌드(클라이언트가 바뀌므로 필수).

## File Structure

| 파일 | 책임 | 상태 |
|---|---|---|
| `client/src/lib/endpoint.ts` | 목록 편집 헬퍼(`upsertEndpoint`/`removeEndpoint`/`normalizeBaseUrl` 공개) | 수정 |
| `client/src/lib/endpoint.test.ts` | 편집 규칙·id 생성·정규화 고정 | 수정 |
| `client/src/lib/api.ts` | 임의 baseUrl의 health 확인(`probeEndpoint`) | 수정 |
| `client/src/components/settings/EndpointSettings.tsx` | 목록·추가·전환·삭제 UI | 생성 |
| `client/src/pages/SettingsPage.tsx` | 카드 배치 | 수정 |

---

### Task 1: 목록 편집 헬퍼

**Files:**
- Modify: `client/src/lib/endpoint.ts`
- Test: `client/src/lib/endpoint.test.ts`

**Interfaces:**
- Produces: `export function normalizeBaseUrl(raw: string): string` (기존 비공개 함수를 공개로)
- Produces: `export function endpointIdFromBaseUrl(baseUrl: string): string`
- Produces: `export function upsertEndpoint(input: { id?: string; label: string; baseUrl: string }): Endpoint`
- Produces: `export function removeEndpoint(id: string): void`

- [ ] **Step 1: 실패하는 테스트 작성**

`client/src/lib/endpoint.test.ts` 끝에 추가한다. 기존 파일의 `equal(...)` 헬퍼를 그대로 쓴다.

```ts
// id는 baseUrl에서 파생한다: 같은 서버를 두 번 등록하면 두 줄이 아니라 한 줄이
// 갱신돼야 한다. 라벨만 다른 중복 엔드포인트는 토큰도 따로 저장돼서, 어느 쪽이
// 로그인돼 있는지 사용자가 알 수 없게 만든다.
equal(endpointIdFromBaseUrl('http://100.1.2.3:33033'), '100-1-2-3-33033', 'id는 호스트+포트에서 파생');
equal(endpointIdFromBaseUrl('http://100.1.2.3:33033/'), '100-1-2-3-33033', '끝 슬래시는 같은 id');

// baseUrl 정규화: 끝 슬래시 제거, 공백 제거. 스킴이 없으면 조립이 깨지므로 그대로 둔다
// (검증은 UI가 하고, 여기서는 문자열 규칙만 고정한다).
equal(normalizeBaseUrl('  http://a.test:1/  '), 'http://a.test:1', '공백과 끝 슬래시 제거');
equal(normalizeBaseUrl(''), '', '빈 값은 현재 오리진을 뜻하므로 보존');

const saved = upsertEndpoint({ label: 'Studio', baseUrl: 'http://100.1.2.3:33033/' });
equal(saved.id, '100-1-2-3-33033', 'upsert는 파생 id를 쓴다');
equal(saved.baseUrl, 'http://100.1.2.3:33033', 'upsert는 정규화해서 저장한다');
equal(saved.capabilities.webPush, false, '원격은 서비스워커가 오리진에 묶여 웹푸시 불가');
equal(saved.capabilities.localFiles, false, '원격 파일은 경로 참조가 아니라 업로드');
equal(listEndpoints().length, 2, 'local + 방금 추가한 하나');

upsertEndpoint({ label: 'Studio (renamed)', baseUrl: 'http://100.1.2.3:33033' });
equal(listEndpoints().length, 2, '같은 baseUrl 재등록은 줄을 늘리지 않는다');
equal(listEndpoints()[1].label, 'Studio (renamed)', '재등록은 라벨을 갱신한다');

removeEndpoint('100-1-2-3-33033');
equal(listEndpoints().length, 1, '삭제하면 local만 남는다');
equal(currentEndpointId(), 'local', '현재 엔드포인트를 지우면 local로 돌아온다');
```

파일 상단 import에 새 이름들을 추가한다:

```ts
import {
  apiUrl, currentEndpointId, endpointIdFromBaseUrl, listEndpoints, localEndpoint,
  normalizeBaseUrl, removeEndpoint, upsertEndpoint, wsUrl,
} from './endpoint';
```

- [ ] **Step 2: 테스트가 타입 검사에서 실패하는지 확인**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: FAIL — `Module '"./endpoint"' has no exported member 'upsertEndpoint'` 등 4건.

- [ ] **Step 3: 구현**

`client/src/lib/endpoint.ts`에서 `normalizeBaseUrl`을 `export`로 바꾸고 아래를 추가한다.

```ts
/** id는 baseUrl에서 파생한다 — 같은 서버가 두 줄로 갈라지면 토큰도 갈라지고,
 *  사용자는 어느 쪽에 로그인돼 있는지 알 수 없게 된다. */
export function endpointIdFromBaseUrl(baseUrl: string): string {
  const normalized = normalizeBaseUrl(baseUrl);
  if (!normalized) return LOCAL_ENDPOINT_ID;
  return normalized
    .replace(/^https?:\/\//, '')
    .replace(/[^a-zA-Z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .toLowerCase();
}

export function upsertEndpoint(input: { id?: string; label: string; baseUrl: string }): Endpoint {
  const baseUrl = normalizeBaseUrl(input.baseUrl);
  const endpoint: Endpoint = {
    id: input.id || endpointIdFromBaseUrl(baseUrl),
    label: input.label.trim() || baseUrl,
    baseUrl,
    // A remote endpoint cannot deliver web push (the service worker is bound to the
    // page's own origin) and its files live on another machine, so a dropped file
    // must be uploaded rather than path-referenced. See spec §7.
    capabilities: { webPush: false, localFiles: false },
  };
  const rest = listEndpoints().filter((e) => e.id !== LOCAL_ENDPOINT_ID && e.id !== endpoint.id);
  saveEndpoints([...rest, endpoint]);
  return endpoint;
}

export function removeEndpoint(id: string) {
  if (id === LOCAL_ENDPOINT_ID) return; // structural, not user data
  saveEndpoints(listEndpoints().filter((e) => e.id !== LOCAL_ENDPOINT_ID && e.id !== id));
  clearTokens(id);
  if (currentEndpointId() === id) setCurrentEndpoint(LOCAL_ENDPOINT_ID);
}
```

- [ ] **Step 4: 타입 검사 통과 확인**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: PASS (출력 없음)

- [ ] **Step 5: 커밋**

```bash
git add client/src/lib/endpoint.ts client/src/lib/endpoint.test.ts
git commit -m "feat(endpoint): 목록 편집 헬퍼 (upsert/remove/파생 id)"
```

---

### Task 2: 저장 전 health 확인

**Files:**
- Modify: `client/src/lib/api.ts`

**Interfaces:**
- Produces: `export async function probeEndpoint(baseUrl: string): Promise<{ ok: boolean; appName?: string; version?: string; authMethod?: string; error?: string }>`

오타와 CORS 미설정을 **저장 전에** 잡기 위한 것이다. 이게 없으면 사용자는 엔드포인트를 저장하고, 전환하고, 리로드한 뒤에야 빈 화면을 만난다 — 그리고 원인이 오타인지 서버 설정인지 알 방법이 없다.

- [ ] **Step 1: 구현**

`api.ts`의 `getAuthConfig` 아래에 추가한다. `apiUrl`을 쓰지 않는 이유는 **아직 등록되지 않은 baseUrl**을 확인해야 하기 때문이다.

```ts
// Checks a base URL BEFORE it is saved. Uses the URL directly rather than apiUrl(),
// because the endpoint does not exist yet. A failure here is almost always one of
// two things, and the message must say which: a typo (network error) or a server
// that does not trust this origin (CORS — the request is blocked before any status
// reaches us, so a TypeError is what a wrong allow-list looks like from here).
export async function probeEndpoint(baseUrl: string): Promise<{
  ok: boolean; appName?: string; version?: string; authMethod?: string; error?: string;
}> {
  const target = baseUrl.trim().replace(/\/+$/, '');
  if (!/^https?:\/\//.test(target)) {
    return { ok: false, error: 'http:// 또는 https:// 로 시작하는 주소여야 합니다.' };
  }
  try {
    const res = await fetch(`${target}/api/auth/health`);
    if (!res.ok) return { ok: false, error: `서버가 HTTP ${res.status}로 응답했습니다.` };
    const data = await res.json();
    return { ok: true, appName: data.appName, version: data.version, authMethod: data.authMethod };
  } catch {
    return {
      ok: false,
      error: '연결하지 못했습니다. 주소가 맞는지, 그리고 그 서버가 이 주소를 '
        + 'CLIENT_ORIGINS에 허용했는지 확인하세요.',
    };
  }
}
```

- [ ] **Step 2: 타입 검사**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: PASS

- [ ] **Step 3: 커밋**

```bash
git add client/src/lib/api.ts
git commit -m "feat(endpoint): 저장 전 health 확인 (probeEndpoint)"
```

---

### Task 3: 설정 카드

**Files:**
- Create: `client/src/components/settings/EndpointSettings.tsx`
- Modify: `client/src/pages/SettingsPage.tsx`

**Interfaces:**
- Produces: `export function EndpointSettings(): JSX.Element`

- [ ] **Step 1: 컴포넌트 작성**

`client/src/components/settings/EndpointSettings.tsx`:

```tsx
import { useState } from 'react';
import { probeEndpoint } from '../../lib/api';
import {
  currentEndpointId, listEndpoints, LOCAL_ENDPOINT_ID, removeEndpoint, setCurrentEndpoint,
  upsertEndpoint, type Endpoint,
} from '../../lib/endpoint';

// Switching servers reloads the page on purpose. Boot mints this endpoint's own
// anonymous token (App.tsx), the socket is opened per endpoint (ws.ts), and every
// agent, trace and pending approval in the store belongs to one server. Swapping
// those in place would leave the previous machine's state on screen next to the new
// machine's — a reload is the honest way to change which machine you are driving.
function switchTo(id: string) {
  setCurrentEndpoint(id);
  window.location.reload();
}

export function EndpointSettings() {
  const [endpoints, setEndpoints] = useState<Endpoint[]>(() => listEndpoints());
  const [adding, setAdding] = useState(false);
  const [label, setLabel] = useState('');
  const [baseUrl, setBaseUrl] = useState('');
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState('');
  const current = currentEndpointId();

  const add = async () => {
    setChecking(true);
    setError('');
    const probe = await probeEndpoint(baseUrl);
    setChecking(false);
    if (!probe.ok) {
      setError(probe.error || '연결하지 못했습니다.');
      return;
    }
    upsertEndpoint({ label: label || probe.appName || baseUrl, baseUrl });
    setEndpoints(listEndpoints());
    setAdding(false);
    setLabel('');
    setBaseUrl('');
  };

  const drop = (id: string) => {
    removeEndpoint(id);
    setEndpoints(listEndpoints());
  };

  return (
    <section className="card p-3">
      <div className="text-sm font-medium">Workstations</div>
      <div className="mt-1 text-xs text-deck-text-dim">
        이 클라이언트가 조종할 서버들입니다. 원격은 Tailscale 같은 사설 네트워크 뒤에 두세요.
      </div>

      <div className="mt-3 space-y-2">
        {endpoints.map((endpoint) => (
          <div key={endpoint.id} className="flex min-w-0 items-center gap-2 rounded-lg border border-deck-border p-2">
            <span className={`h-2 w-2 shrink-0 rounded-full ${endpoint.id === current ? 'bg-deck-success' : 'bg-deck-text-faint'}`} />
            <div className="min-w-0 flex-1">
              <div className="truncate text-xs font-medium">{endpoint.label}</div>
              <div className="truncate text-[10px] text-deck-text-dim">
                {endpoint.baseUrl || '이 페이지를 서빙한 서버'}
              </div>
            </div>
            {endpoint.id === current ? (
              <span className="shrink-0 text-[10px] text-deck-success">사용 중</span>
            ) : (
              <button
                type="button"
                onClick={() => switchTo(endpoint.id)}
                className="min-h-8 shrink-0 rounded-lg border border-deck-border px-2 text-[11px] text-deck-accent-light"
              >
                전환
              </button>
            )}
            {endpoint.id !== LOCAL_ENDPOINT_ID && (
              <button
                type="button"
                onClick={() => drop(endpoint.id)}
                className="min-h-8 shrink-0 rounded-lg px-2 text-[11px] text-deck-text-dim hover:text-deck-danger"
                title="이 워크스테이션과 저장된 토큰을 지웁니다"
              >
                삭제
              </button>
            )}
          </div>
        ))}
      </div>

      {adding ? (
        <div className="mt-3 space-y-2">
          <input
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            placeholder="이름 (예: Mac Studio)"
            className="min-h-9 w-full rounded-lg border border-deck-border bg-deck-surface px-2 text-xs"
          />
          <input
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            placeholder="http://100.x.y.z:33033"
            autoCapitalize="off"
            autoCorrect="off"
            spellCheck={false}
            className="min-h-9 w-full rounded-lg border border-deck-border bg-deck-surface px-2 text-xs"
          />
          {error && <div className="text-[11px] leading-relaxed text-deck-danger">{error}</div>}
          <div className="flex gap-2">
            <button
              type="button"
              disabled={checking || !baseUrl.trim()}
              onClick={() => void add()}
              className="min-h-9 rounded-lg bg-deck-accent px-3 text-xs text-white disabled:opacity-40"
            >
              {checking ? '확인 중…' : '확인 후 추가'}
            </button>
            <button
              type="button"
              onClick={() => { setAdding(false); setError(''); }}
              className="min-h-9 rounded-lg border border-deck-border px-3 text-xs text-deck-text-dim"
            >
              취소
            </button>
          </div>
        </div>
      ) : (
        <button
          type="button"
          onClick={() => setAdding(true)}
          className="mt-3 min-h-9 rounded-lg border border-deck-border px-3 text-xs text-deck-accent-light"
        >
          워크스테이션 추가
        </button>
      )}
    </section>
  );
}
```

- [ ] **Step 2: 설정 화면에 배치**

`client/src/pages/SettingsPage.tsx`의 import에 추가:

```tsx
import { EndpointSettings } from '../components/settings/EndpointSettings';
```

`<main>` 안, `<UiScaleSettings />` **앞에** 놓는다. 어느 기계를 보고 있는지가 나머지 설정의 전제이기 때문이다:

```tsx
        <EndpointSettings />
        <UiScaleSettings />
```

- [ ] **Step 3: 타입 검사**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: PASS

- [ ] **Step 4: 커밋**

```bash
git add client/src/components/settings/EndpointSettings.tsx client/src/pages/SettingsPage.tsx
git commit -m "feat(settings): 워크스테이션(엔드포인트) 카드"
```

---

### Task 4: 원격일 때 성립하지 않는 것을 화면에서 정직하게 말한다

**Files:**
- Modify: `client/src/components/settings/NotificationSettings.tsx`

원격 엔드포인트에서는 웹푸시가 **성립하지 않는다.** 서비스워커가 페이지 오리진에 묶여 있어서, 원격 서버는 이 브라우저에 푸시를 보낼 수 없다(스펙 §6). 지금 화면은 그걸 모르고 켜기 버튼을 보여준다 — 켜면 조용히 아무 일도 일어나지 않는다.

- [ ] **Step 1: 현재 구조 확인**

Run: `grep -n "export function NotificationSettings\|return (" client/src/components/settings/NotificationSettings.tsx | head -4`
Expected: `export function NotificationSettings()`(~line 9)과 그 안의 `return (`(~line 40)이 보인다. 훅 호출은 전부 그 `return` 위에 있다.

- [ ] **Step 2: 원격이면 안내로 대체**

컴포넌트 최상단에 추가한다:

```tsx
import { currentEndpoint } from '../../lib/endpoint';
```

그리고 **`return (` 바로 앞**(모든 훅 호출 뒤)에 분기를 넣는다. 훅보다 앞에 두면 조건부 훅 호출이 되어 React가 깨진다. **기존 렌더 경로는 건드리지 않는다** — 로컬 엔드포인트에서는 오늘과 같아야 한다.

```tsx
  // Web push needs a service worker, and a service worker is bound to the origin
  // that served this page. A remote workstation therefore cannot deliver it — the
  // desktop shell uses native notifications there instead (spec §6). Saying so is
  // better than a toggle that turns on and never fires.
  if (!currentEndpoint().capabilities.webPush) {
    return (
      <section className="card p-3">
        <div className="text-sm font-medium">알림</div>
        <div className="mt-1 text-xs leading-relaxed text-deck-text-dim">
          원격 워크스테이션에서는 웹 푸시를 쓸 수 없습니다. 서비스 워커가 이 페이지를 서빙한
          오리진에 묶여 있기 때문입니다. 데스크탑 앱에서는 네이티브 알림으로 대체됩니다.
        </div>
      </section>
    );
  }
```

- [ ] **Step 3: 타입 검사 + 커밋**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: PASS

```bash
git add client/src/components/settings/NotificationSettings.tsx
git commit -m "feat(settings): 원격에서 웹푸시가 불가함을 명시"
```

---

### Task 5: 실물 확인 · 바이너리

- [ ] **Step 1: 두 서버 띄우기**

```bash
cd server && CGO_ENABLED=0 go build -o /tmp/pcd-a . && cd ..
POWERCODEDECK_PORT=33033 POWERCODEDECK_DB_PATH=/tmp/a.db /tmp/pcd-a &
POWERCODEDECK_PORT=33044 POWERCODEDECK_DB_PATH=/tmp/b.db \
  POWERCODEDECK_CLIENT_ORIGINS=http://localhost:33033 /tmp/pcd-a &
```

두 번째 서버가 첫 번째의 오리진을 허용해야 한다. **이 한 줄을 빠뜨리면 추가 버튼이 CORS로 실패하고, 그게 정확히 Task 2의 에러 메시지가 안내해야 하는 상황이다.**

- [ ] **Step 2: 다섯 가지 확인**

`http://localhost:33033/settings`를 열고:

1. Workstations 카드에 `This machine` 한 줄과 `사용 중` 표시
2. 추가 → `http://localhost:33044` → "확인 후 추가"가 **성공**하고 목록이 두 줄
3. 오타 주소(`http://localhost:39999`)로 추가 → **명확한 에러**가 뜨고 목록은 그대로
4. 두 번째 줄에서 "전환" → 리로드 후 **에이전트 목록이 33044의 것**으로 바뀐다
5. 알림 카드가 "원격에서는 웹 푸시 불가" 안내로 바뀐다

- [ ] **Step 3: 삭제와 폴백 확인**

원격 엔드포인트를 사용 중인 상태에서 삭제 → `local`로 돌아오고, `localStorage`에서 그 엔드포인트의 토큰 키가 사라진다.

Run (브라우저 콘솔): `Object.keys(localStorage).filter(k => k.includes('endpoints'))`
Expected: 삭제한 엔드포인트의 `accessToken`/`refreshToken` 키가 없다.

- [ ] **Step 4: 회귀 확인**

엔드포인트를 하나도 추가하지 않은 브라우저 프로필로 열어 오늘과 똑같이 동작하는지 본다. 카드는 한 줄, 알림 설정은 원래대로.

- [ ] **Step 5: `dist/pcd.exe` 재빌드**

```bash
cd client && ./node_modules/.bin/vite build && cd ..
rm -rf server/static && cp -r client/dist server/static
cd server && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o ../dist/pcd.exe .
```

- [ ] **Step 6: 커밋**

```bash
git add dist/pcd.exe
git commit -m "chore: pcd.exe 재빌드 (엔드포인트 UI)"
```

## Self-Review

- **전환에 리로드를 쓰는 것이 이 계획의 유일한 논쟁점이다.** 부분 갱신으로 하면 스토어(에이전트·트레이스·승인 큐)와 WS와 토큰을 전부 동시에 갈아끼워야 하고, 하나라도 놓치면 옛 서버의 잔상이 새 화면에 남는다. 리로드는 그 실패 모드를 구조적으로 없앤다.
- **capabilities를 원격에서 false로 고정하는 것은 추정이 아니라 사실이다.** 서비스워커는 오리진에 묶이고, 원격 파일은 다른 기계에 있다. 나중에 데스크탑 셸이 생기면 셸이 이 값을 덮어쓴다.
- **삭제가 토큰까지 지우는 것이 중요하다.** 안 지우면 같은 주소를 다시 등록했을 때 죽은 토큰으로 401 루프에 빠진다.
- 이 계획은 인증을 강화하지 않는다. 노출도 파생 기동 정책은 별도 계획이다.
