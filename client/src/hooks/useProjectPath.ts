import { useParams, useSearchParams } from 'react-router-dom';

// The project path of /launch: ?path=… (current links), or the old
// /launch/:encodedPath form still reached by in-app history entries.
export function useProjectPath(): string {
  const [params] = useSearchParams();
  const { encodedPath } = useParams<{ encodedPath: string }>();
  return params.get('path') ?? decodeURIComponent(encodedPath || '');
}
