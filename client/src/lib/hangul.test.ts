// Framework-free assertions (type-checked by tsc like the other *.test.ts files).
import { HangulComposer, jamoFor } from './hangul';

function equal(actual: unknown, expected: unknown, label: string) {
  if (actual !== expected) throw new Error(`${label}: expected ${String(expected)}, got ${String(actual)}`);
}

// Types QWERTY keys (uppercase = shifted) with Backspace as '<', and returns the
// committed text followed by the syllable still in progress.
function typeKeys(keys: string): string {
  const c = new HangulComposer();
  let out = '';
  for (const k of keys) {
    if (k === '<') {
      if (!c.backspace().handled) out = out.slice(0, -1);
      continue;
    }
    const jamo = jamoFor(k, k !== k.toLowerCase());
    if (!jamo) throw new Error(`no jamo for ${k}`);
    out += c.type(jamo).commit;
  }
  return out + c.preedit;
}

equal(typeKeys('gksrmf'), '한글', 'two syllables');
equal(typeKeys('dkssudgktpdy'), '안녕하세요', 'final consonant moves to the next syllable');
equal(typeKeys('rkrk'), '가가', '각 + ㅏ → 가가');
equal(typeKeys('ekfr'), '닭', 'compound final ㄺ');
equal(typeKeys('ekfrdl'), '닭이', 'ㅇ after a compound final starts a new syllable');
equal(typeKeys('ekfrk'), '달가', 'a vowel splits a compound final, keeping its first part');
equal(typeKeys('rhk'), '과', 'compound vowel ㅘ');
equal(typeKeys('dml'), '의', 'compound vowel ㅢ');
equal(typeKeys('Rk'), '까', 'shift gives a double initial');
equal(typeKeys('dhk'), '와', 'ㅇ + ㅘ');
equal(typeKeys('rkE'), '가ㄸ', 'ㄸ cannot be a final, so it starts its own syllable');
equal(typeKeys('rksk'), '가나', 'ㄴ moves on');
equal(typeKeys('hk'), 'ㅘ', 'vowels alone combine');
equal(typeKeys('kk'), 'ㅏㅏ', 'two vowels that do not combine');
equal(typeKeys('rt'), 'ㄱㅅ', 'consonants alone do not form a final');
equal(typeKeys('qkqh'), '바보', 'ㅂ final moves on');
equal(typeKeys('tkfkd'), '사랑', 'ending in a final');
equal(typeKeys('qkfqtk'), '밟사', 'a consonant that cannot join ㄼ starts the next syllable');

// Backspace takes back one jamo of the syllable in progress, then committed text.
equal(typeKeys('ekfr<'), '달', 'backspace removes the last final part');
equal(typeKeys('ekfr<<<'), 'ㄷ', 'backspace walks back to the initial');
equal(typeKeys('rhk<'), '고', 'backspace undoes the second half of a compound vowel');
equal(typeKeys('gksrmf<<<'), '한', 'backspace past the syllable deletes committed text');

// Flush ends the syllable; the next jamo starts fresh.
const c = new HangulComposer();
c.type('ㄱ'); c.type('ㅏ');
equal(c.flush(), '가', 'flush returns the syllable');
equal(c.active, false, 'flush clears the composer');
equal(c.type('ㄴ').commit, '', 'nothing left to commit after a flush');
