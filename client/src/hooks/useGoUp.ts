import { useCallback } from 'react';
import { useNavigate } from 'react-router-dom';

/**
 * 계층("위로") 네비게이션. 각 화면은 부모를 정확히 하나 선언하고, 뒤로 가기는
 * 진입 경로와 무관하게 항상 그 부모로 간다.
 *
 * 이전 구현(useGoBack)은 navigate(-1), 즉 "마지막 이동 취소"였다. 사용자가 뒤로
 * 가기에 기대하는 "한 단계 위로"와는 일직선으로 들어왔을 때만 우연히 일치한다.
 * 실제로는 두 가지가 어긋났다:
 *   - BottomNav 탭 이동이 히스토리에 쌓여, 터미널 → 설정 → 로그에서 뒤로를 누르면
 *     작업하던 터미널이 아니라 설정으로 갔다
 *   - 관제실 진입 경로가 둘이라, 관제실 → 터미널 → (헤더 아이콘) 관제실 → 뒤로가
 *     터미널로 되돌아오는 루프가 생겼다
 *
 * 계층 방식은 히스토리를 보지 않으므로 딥링크와 PWA 콜드 스타트에서도 동일하게
 * 동작한다(예전에는 idx === 0이라 fallback으로 샜다).
 *
 * 대가: 터미널 → 로그 → 뒤로는 그 터미널이 아니라 /control로 간다. 주 루프가
 * 벽↔세션이고 로그·설정은 간헐적 경유지라 클릭 한 번을 받아들인 결정이다.
 */
export function useGoUp(parent: string) {
  const navigate = useNavigate();
  return useCallback(() => navigate(parent), [navigate, parent]);
}
