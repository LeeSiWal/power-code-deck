/**
 * The visible conversation and spoken narration are different projections
 * of the same agent turn. Never rewrite or truncate the visible transcript.
 * Voice is intentionally a conservative summary; code, logs and secrets are
 * left on screen rather than read aloud.
 */
export type Narration = { text: string; requiresScreen: boolean };

export function toVoiceNarration(message: string, maxLength = 320): Narration {
  if (!message.trim()) return { text: '', requiresScreen: false };
  let requiresScreen = false;
  let text = message
    .replace(/```[\s\S]*?```/g, () => { requiresScreen = true; return ' 코드 내용은 화면에서 확인할 수 있어요. '; })
    .replace(/\`[^\`\n]{1,180}\`/g, (value) => value.slice(1,-1))
    .replace(/https?:\/\/[^\s)]+/g, () => { requiresScreen = true; return '관련 링크'; })
    .replace(/(?:^|\n)\s*(?:at\s+\S+|Traceback \(most recent call last\)|\$\s+\S+)[^\n]*/gm, () => {
      requiresScreen = true; return ' 자세한 실행 기록은 화면에서 확인할 수 있어요. ';
    })
    .replace(/(?:^|\n)\s*\|[^\n]+\|/gm, () => { requiresScreen = true; return ' '; })
    .replace(/\s+/g, ' ').trim();

  if (text.length > maxLength) {
    requiresScreen = true;
    const cut = text.slice(0, Math.max(40, maxLength));
    const boundary = Math.max(cut.lastIndexOf('. '), cut.lastIndexOf('요. '), cut.lastIndexOf('다. '));
    text = boundary > 70 ? cut.slice(0, boundary + 1) : cut.replace(/\s+\S*$/, '');
    text += ' 자세한 내용은 화면에 있어요.';
  }
  return { text, requiresScreen };
}
