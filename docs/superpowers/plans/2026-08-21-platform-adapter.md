# platform 어댑터 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 브라우저에만 있는 능력(알림·외부 링크·파일 선택·자격증명 저장)을 **한 인터페이스 뒤로 모은다.** 데스크탑 셸은 나중에 이 인터페이스만 갈아끼우면 된다.

**Architecture:** 스펙 §6이 데스크탑 셸의 **가장 큰 리스크를 기술이 아니라 클라이언트 포크**로 꼽는다. React 코드베이스는 하나여야 하고, 셸은 얇은 어댑터 하나만 대체해야 한다.

그래서 셸보다 어댑터를 **먼저** 만든다. 지금은 브라우저 구현밖에 없으므로 이 계획은 겉보기에 아무 기능도 더하지 않는다 — 순수한 리팩터링이다. 그 대신 이 작업이 끝나면 "브라우저에서만 되는 것"이 코드베이스 전체에 흩어져 있지 않고 파일 하나에 모여 있어서, 셸 계획이 그 파일의 목록을 그대로 작업 목록으로 쓸 수 있다.

**YAGNI:** 스펙은 `spawnLocal`(로컬 호스트 기동)도 어댑터에 든다고 적었지만, **브라우저 구현이 존재할 수 없고 호출부도 없다.** 셸이 생길 때 추가한다. 지금 넣으면 아무도 부르지 않는 인터페이스가 남는다.

**Tech Stack:** TypeScript. 새 npm 패키지 금지, 새 런타임 의존성 금지.

## Global Constraints

- 스펙: `docs/superpowers/specs/2026-08-21-desktop-and-remote-design.md` §6.
- **동작이 달라지면 실패다.** 이 계획은 리팩터링이고, 브라우저에서의 결과는 이전과 바이트 단위로 같아야 한다.
- 새 npm 패키지 금지.
- 클라이언트 검증: `cd client && ./node_modules/.bin/tsc --noEmit`
- 클라이언트 테스트 러너는 없다(`endpoint.test.ts` 관례 — 모듈 스코프 단언, `tsc`로 타입만 검사). 러너를 도입하지 말 것.
- 마지막에 `dist/pcd.exe` 재빌드.

## File Structure

| 파일 | 책임 | 상태 |
|---|---|---|
| `client/src/platform/types.ts` | `Platform` 인터페이스 (구현 없음) | 생성 |
| `client/src/platform/browser.ts` | 브라우저 구현 | 생성 |
| `client/src/platform/index.ts` | 현재 플랫폼 선택 + `platform` export | 생성 |
| `client/src/hooks/useAgentNotification.ts` | `new Notification` → `platform.notify` | 수정 |
| `client/src/components/terminal/TerminalView.tsx` | `window.open` → `platform.openExternal` | 수정 |
| `client/src/components/native/NativeChat.tsx` | 파일 선택 → `platform.pickFiles` | 수정 |
| `client/src/lib/endpoint.ts` | localStorage 접근 → `platform.secureStorage` | 수정 |

---

### Task 1: 인터페이스

**Files:**
- Create: `client/src/platform/types.ts`

**Interfaces:**
- Produces: `export interface Platform { name; notify; openExternal; pickFiles; secureStorage }`

- [ ] **Step 1: 작성**

`client/src/platform/types.ts`:

```ts
// What a host can do for us that the app itself cannot.
//
// This exists to keep ONE React codebase across browser and desktop shells. The
// spec's biggest named risk for the desktop app is a client fork (§6), and a fork
// starts exactly where a component reaches for `window` directly. Everything the
// browser is special about lives behind this interface, so a shell replaces one
// file instead of touching components.
//
// The browser implementation is always the reference. A shell OVERRIDES; it never
// defines a capability the browser lacks — if it needs one, it goes here first with
// an honest browser fallback.

export interface NotifyOptions {
  title: string;
  body?: string;
  /** Groups replaceable notifications; a later one with the same tag supersedes. */
  tag?: string;
  /** Keeps the notification on screen until the user acts on it. Used today for
   *  permission requests, which are the one kind you must not miss. */
  requireInteraction?: boolean;
  /** Extra work when the user activates it. Focusing the app and dismissing the
   *  notification is the host's job and happens either way. */
  onActivate?: () => void;
}

export interface PickedFile {
  name: string;
  file: File;
}

/** Where credentials live. The browser has localStorage; a shell has an OS keychain. */
export interface SecureStorage {
  get(key: string): string | null;
  set(key: string, value: string): void;
  remove(key: string): void;
}

export interface Platform {
  /** 'browser' today; 'tauri' when a shell is added. Shown in diagnostics. */
  readonly name: string;

  /** Raises a notification the user sees outside this tab. Returns false when the
   *  host cannot (permission denied, unsupported) so callers can stay quiet rather
   *  than pretend it worked. */
  notify(options: NotifyOptions): boolean;

  /** Opens a URL outside the app. In a shell this must reach the system browser —
   *  a webview navigating away from the app is a dead end with no back button. */
  openExternal(url: string): void;

  /** Asks the user for files. Resolves empty when they cancel. */
  pickFiles(options?: { multiple?: boolean; accept?: string }): Promise<PickedFile[]>;

  readonly secureStorage: SecureStorage;
}
```

- [ ] **Step 2: 타입 검사 + 커밋**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: PASS

```bash
git add client/src/platform/types.ts
git commit -m "feat(platform): 호스트 능력 인터페이스"
```

---

### Task 2: 브라우저 구현

**Files:**
- Create: `client/src/platform/browser.ts`, `client/src/platform/index.ts`

- [ ] **Step 1: 브라우저 구현 작성**

`client/src/platform/browser.ts`:

```ts
import type { NotifyOptions, PickedFile, Platform, SecureStorage } from './types';

// localStorage throws in some privacy modes and can be evicted on iOS. Every access
// is guarded, because a storage failure must degrade to "not remembered" rather than
// take down the screen that was trying to read it.
const browserStorage: SecureStorage = {
  get(key) {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  set(key, value) {
    try {
      localStorage.setItem(key, value);
    } catch {
      /* storage disabled — the session still works, it just won't be remembered */
    }
  },
  remove(key) {
    try {
      localStorage.removeItem(key);
    } catch {
      /* ignore */
    }
  },
};

export const browserPlatform: Platform = {
  name: 'browser',

  notify({ title, body, tag, requireInteraction, onActivate }: NotifyOptions) {
    if (!('Notification' in window) || Notification.permission !== 'granted') return false;
    const notification = new Notification(title, { body, tag, requireInteraction });
    // Focus + close on click is what the app does today; keep it unconditional so
    // this refactor changes nothing the user can observe.
    notification.onclick = () => {
      window.focus();
      onActivate?.();
      notification.close();
    };
    return true;
  },

  openExternal(url: string) {
    window.open(url, '_blank', 'noopener,noreferrer');
  },

  pickFiles({ multiple = false, accept } = {}): Promise<PickedFile[]> {
    return new Promise((resolve) => {
      const input = document.createElement('input');
      input.type = 'file';
      input.multiple = multiple;
      if (accept) input.accept = accept;
      input.style.display = 'none';
      // Cancel fires on modern browsers; without the fallback the promise would hang
      // forever on those that do not, leaking a pending await per cancelled dialog.
      const done = (files: PickedFile[]) => {
        input.remove();
        resolve(files);
      };
      input.onchange = () => done(
        Array.from(input.files || []).map((file) => ({ name: file.name, file })),
      );
      input.oncancel = () => done([]);
      document.body.appendChild(input);
      input.click();
    });
  },

  secureStorage: browserStorage,
};
```

- [ ] **Step 2: 선택기 작성**

`client/src/platform/index.ts`:

```ts
import { browserPlatform } from './browser';
import type { Platform } from './types';

export type { NotifyOptions, PickedFile, Platform, SecureStorage } from './types';

// One place decides which host we are running on. A Tauri shell injects
// `window.__TAURI_INTERNALS__`, so detection stays a property check rather than a
// build flag — the same bundle has to run in both.
function detect(): Platform {
  return browserPlatform;
}

export const platform: Platform = detect();
```

`detect()`가 지금은 분기하지 않는 것이 의도적이다. 셸 계획이 여기 한 곳에 분기를 넣는다.

- [ ] **Step 3: 타입 검사 + 커밋**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: PASS

```bash
git add client/src/platform/browser.ts client/src/platform/index.ts
git commit -m "feat(platform): 브라우저 구현과 선택기"
```

---

### Task 3: 알림 호출부 이전

**Files:**
- Modify: `client/src/hooks/useAgentNotification.ts:66`

- [ ] **Step 1: 현재 코드 확인**

Run: `sed -n '64,71p' client/src/hooks/useAgentNotification.ts`
Expected:

```ts
        if (document.hidden && Notification.permission === 'granted') {
          const n = new Notification(`PowerCodeDeck: ${result.reason}`, {
            body: result.message,
            tag: `powercodedeck-${agentId}-${result.reason}`,
            requireInteraction: result.reason === 'permission_request',
          });
          n.onclick = () => { window.focus(); n.close(); };
        }
```

- [ ] **Step 2: 교체**

import 추가:

```ts
import { platform } from '../platform';
```

블록을 통째로 바꾼다. **문자열과 조건을 한 글자도 바꾸지 않는다** — 이 태스크는 옮기기만 한다. `document.hidden` 검사는 남는다(호스트 능력이 아니라 앱 정책이고, 셸에서는 다른 판단을 하게 된다). 포커스·닫기는 어댑터가 하므로 여기서 사라진다:

```ts
        if (document.hidden) {
          platform.notify({
            title: `PowerCodeDeck: ${result.reason}`,
            body: result.message,
            tag: `powercodedeck-${agentId}-${result.reason}`,
            requireInteraction: result.reason === 'permission_request',
          });
        }
```

`Notification.permission !== 'granted'` 검사도 사라진다 — 어댑터가 같은 검사를 하고 `false`를 돌려준다. 호출부가 브라우저 권한 모델을 알 필요가 없어야 셸이 그 자리를 대체할 수 있다.

- [ ] **Step 3: 타입 검사**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: PASS

- [ ] **Step 4: 손으로 확인**

덱을 띄우고 알림 권한을 허용한 뒤, **다른 탭으로 옮겨**(`document.hidden`이 참이어야 한다) 에이전트가 승인 요청을 내게 한다. 알림이 뜨고, 승인 요청 알림은 저절로 사라지지 않아야 하며(`requireInteraction`), 클릭하면 창이 포커스되고 알림이 닫혀야 한다.

- [ ] **Step 5: 커밋**

```bash
git add client/src/hooks/useAgentNotification.ts
git commit -m "refactor(platform): 알림을 어댑터 뒤로"
```

---

### Task 4: 외부 링크 호출부 이전

**Files:**
- Modify: `client/src/components/terminal/TerminalView.tsx:777`

- [ ] **Step 1: 교체**

import 추가:

```ts
import { platform } from '../../platform';
```

```ts
      platform.openExternal(url);
```

- [ ] **Step 2: 타입 검사 + 확인 + 커밋**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: PASS

터미널 출력의 링크를 클릭해 새 탭이 열리는지 확인한다.

```bash
git add client/src/components/terminal/TerminalView.tsx
git commit -m "refactor(platform): 외부 링크를 어댑터 뒤로"
```

---

### Task 5: 파일 선택 호출부 이전

**Files:**
- Modify: `client/src/components/native/NativeChat.tsx` (`fileRef` / `onFilePick` / `:1026`의 숨은 input)

여기는 단순 교체가 아니다. 지금은 **숨은 `<input type="file">` 엘리먼트 + ref + onChange 핸들러** 세 조각으로 되어 있고, 어댑터는 그 셋을 한 번의 `await`로 대체한다.

- [ ] **Step 1: 현재 코드 확인**

Run: `grep -n "fileRef\|onFilePick" client/src/components/native/NativeChat.tsx`
Expected: ref 선언, 첨부 버튼의 `fileRef.current?.click()`, `onFilePick` 핸들러, 숨은 input 네 곳.

- [ ] **Step 2: 핸들러를 어댑터 호출로 바꾼다**

`onFilePick`이 `event.target.files`를 읽어 업로드하던 것을, 파일 배열을 받는 함수로 바꾼다:

```tsx
  const attachFiles = useCallback(async () => {
    const picked = await platform.pickFiles({ multiple: true });
    if (!picked.length) return;
    setUploading(true);
    try {
      for (const item of picked) {
        const saved = await api.attachFile(agentId, item.file);
        setAttachments((prev) => [...prev, saved]);
      }
    } catch (err) {
      setError('업로드 실패: ' + String(err));
    } finally {
      setUploading(false);
    }
  }, [agentId]);
```

**주의:** 기존 `onFilePick`이 하던 것과 동작이 같아야 한다. Step 1에서 읽은 원본의 에러 처리·상태 갱신 순서를 그대로 따르고, 다른 점은 파일이 어디서 오는가뿐이어야 한다.

- [ ] **Step 3: 버튼과 숨은 input 정리**

첨부 버튼의 `onClick={() => fileRef.current?.click()}`을 `onClick={() => void attachFiles()}`로 바꾸고, `:1026`의 `<input ref={fileRef} type="file" … />`와 `fileRef` 선언을 **지운다.** 어댑터가 엘리먼트를 만들고 지운다.

- [ ] **Step 4: 타입 검사**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: PASS — 남아 있는 `fileRef` 참조가 있으면 여기서 잡힌다.

- [ ] **Step 5: 손으로 확인**

네이티브 채팅에서 파일 두 개를 첨부하고, 취소도 해본다(취소 시 아무 일도 없어야 하고 업로드 스피너가 걸리면 안 된다).

- [ ] **Step 6: 커밋**

```bash
git add client/src/components/native/NativeChat.tsx
git commit -m "refactor(platform): 파일 선택을 어댑터 뒤로"
```

---

### Task 6: 자격증명 저장 이전

**Files:**
- Modify: `client/src/lib/endpoint.ts`

`endpoint.ts`에는 이미 `safeGet`/`safeSet`/`safeRemove`가 있다 — 어댑터의 `secureStorage`와 **똑같은 일을 하는 사본**이다. 하나로 합친다. 셸에서 토큰이 localStorage가 아니라 OS 키체인에 들어가야 하므로(스펙 §6), 이 이전이 셸의 전제다.

- [ ] **Step 1: 교체**

import 추가:

```ts
import { platform } from '../platform';
```

`safeGet`/`safeSet`/`safeRemove` 세 함수를 **지우고** 호출부를 바꾼다:

```ts
const safeGet = (key: string) => platform.secureStorage.get(key);
const safeSet = (key: string, value: string) => platform.secureStorage.set(key, value);
const safeRemove = (key: string) => platform.secureStorage.remove(key);
```

이름을 유지하는 이유: 이 파일 안의 호출부가 여러 곳이고, 이 태스크의 목적은 **저장 구현을 옮기는 것**이지 호출부를 다시 쓰는 것이 아니다.

- [ ] **Step 2: 순환 import가 없는지 확인**

`platform/*`는 `lib/*`를 import하지 않아야 한다. 확인:

Run: `grep -rn "from '\.\./lib\|from '\.\./\.\./lib" client/src/platform/`
Expected: 출력 없음

- [ ] **Step 3: 타입 검사 + 커밋**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: PASS

```bash
git add client/src/lib/endpoint.ts
git commit -m "refactor(platform): 토큰 저장을 어댑터 뒤로"
```

---

### Task 7: 경계를 테스트로 못 박는다

**Files:**
- Create: `client/src/platform/platform.test.ts`

- [ ] **Step 1: 단언 파일 작성**

`endpoint.test.ts`와 같은 관례를 따른다(모듈 스코프 단언, `tsc`로 타입만 검사, 실행되지 않음).

```ts
import { platform } from './index';
import type { Platform } from './types';

function equal(actual: unknown, expected: unknown, label: string) {
  if (actual !== expected) throw new Error(`${label}: expected ${String(expected)}, got ${String(actual)}`);
}

// The whole point of the adapter is that a shell can replace it. If a capability
// disappears from the interface, this stops compiling — which is the signal we want
// before a component starts reaching for `window` again.
const required: (keyof Platform)[] = ['name', 'notify', 'openExternal', 'pickFiles', 'secureStorage'];
for (const key of required) {
  equal(typeof platform[key] !== 'undefined', true, `platform.${String(key)} exists`);
}

equal(platform.name, 'browser', '브라우저 번들에서는 브라우저 구현이 선택된다');
equal(typeof platform.notify, 'function', 'notify는 함수');
equal(typeof platform.secureStorage.get, 'function', 'secureStorage.get은 함수');
```

- [ ] **Step 2: 흩어진 접근이 남아 있지 않은지 확인**

Run:

```bash
grep -rn "new Notification\|window.open" client/src --include=*.ts --include=*.tsx | grep -v "src/platform/"
```

Expected: 출력 없음. **하나라도 남아 있으면 그 파일이 셸에서 갈라질 첫 번째 파일이다.**

`localStorage` 직접 접근은 아직 남아 있어도 된다(UI 배율·모드 기억 등 호스트와 무관한 값). 자격증명만 어댑터를 거치면 된다.

- [ ] **Step 3: 타입 검사 + 커밋**

Run: `cd client && ./node_modules/.bin/tsc --noEmit`
Expected: PASS

```bash
git add client/src/platform/platform.test.ts
git commit -m "test(platform): 어댑터 경계 고정"
```

---

### Task 8: 회귀 확인 · 바이너리

- [ ] **Step 1: 네 가지가 이전과 같은지 확인**

빌드해서 실제로 띄우고:

1. 알림 — 권한 허용 후 승인 요청 시 뜨고, 클릭하면 세션으로 이동
2. 터미널 링크 클릭 — 새 탭
3. 파일 첨부 — 다중 선택, 취소 시 무반응
4. 로그아웃/재로그인 — 토큰이 저장되고 새로고침해도 유지

- [ ] **Step 2: 재빌드**

```bash
cd client && ./node_modules/.bin/vite build && cd ..
rm -rf server/static && cp -r client/dist server/static
cd server && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o ../dist/pcd.exe .
```

- [ ] **Step 3: 커밋**

```bash
git add dist/pcd.exe
git commit -m "chore: pcd.exe 재빌드 (platform 어댑터)"
```

## Self-Review

- **이 계획은 기능을 더하지 않는다.** 그게 맞다 — 셸이 붙기 전에 포크 위험을 없애는 것이 목적이고, 결과는 `grep`으로 검증된다(Task 7 Step 2).
- **`spawnLocal`을 넣지 않았다.** 브라우저 구현이 존재할 수 없고 호출부도 없다. 셸 계획이 추가한다.
- **`detect()`가 분기하지 않는 것이 의도적이다.** 지금 Tauri 감지를 넣으면 검증할 수 없는 코드가 된다. 셸 계획이 그 한 줄을 넣고 그때 검증한다.
- **`endpoint.ts`의 `safeGet` 사본을 지우는 것이 실제 이득이다.** 같은 방어 로직이 두 벌 있으면 반드시 갈라진다.
- 위험: Task 5(파일 선택)가 유일하게 구조가 바뀌는 곳이다. 취소 경로(`oncancel`)를 빠뜨리면 promise가 영영 안 풀린다 — 그래서 Step 5에서 취소를 명시적으로 확인한다.
