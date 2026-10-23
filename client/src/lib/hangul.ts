// Two-set (두벌식) Hangul composition for the deck keyboard. The deck keyboard
// replaces the iOS soft keyboard, so it cannot borrow the system IME: it types
// compatibility jamo and this turns them into syllables, the way a desktop IME
// does — including the final consonant that moves on to the next syllable
// ("각" + ㅏ → "가가") and compound vowels/finals (ㅘ, ㄳ).

/** QWERTY key → jamo, as printed on a Korean two-set keyboard. */
export const DUBEOLSIK: Record<string, string> = {
  q: 'ㅂ', w: 'ㅈ', e: 'ㄷ', r: 'ㄱ', t: 'ㅅ', y: 'ㅛ', u: 'ㅕ', i: 'ㅑ', o: 'ㅐ', p: 'ㅔ',
  a: 'ㅁ', s: 'ㄴ', d: 'ㅇ', f: 'ㄹ', g: 'ㅎ', h: 'ㅗ', j: 'ㅓ', k: 'ㅏ', l: 'ㅣ',
  z: 'ㅋ', x: 'ㅌ', c: 'ㅊ', v: 'ㅍ', b: 'ㅠ', n: 'ㅜ', m: 'ㅡ',
};

/** Shifted keys that differ from the unshifted jamo. */
export const DUBEOLSIK_SHIFT: Record<string, string> = {
  q: 'ㅃ', w: 'ㅉ', e: 'ㄸ', r: 'ㄲ', t: 'ㅆ', o: 'ㅒ', p: 'ㅖ',
};

/** The jamo a key types, or undefined for a key with no Hangul. */
export function jamoFor(key: string, shift: boolean): string | undefined {
  const k = key.toLowerCase();
  return (shift && DUBEOLSIK_SHIFT[k]) || DUBEOLSIK[k];
}

const CHO = 'ㄱㄲㄴㄷㄸㄹㅁㅂㅃㅅㅆㅇㅈㅉㅊㅋㅌㅍㅎ';
const JUNG = 'ㅏㅐㅑㅒㅓㅔㅕㅖㅗㅘㅙㅚㅛㅜㅝㅞㅟㅠㅡㅢㅣ';
const JONG = ' ㄱㄲㄳㄴㄵㄶㄷㄹㄺㄻㄼㄽㄾㄿㅀㅁㅂㅄㅅㅆㅇㅈㅊㅋㅌㅍㅎ'; // index 0 = none

const VOWEL_PAIRS: Record<string, string> = {
  'ㅗㅏ': 'ㅘ', 'ㅗㅐ': 'ㅙ', 'ㅗㅣ': 'ㅚ', 'ㅜㅓ': 'ㅝ', 'ㅜㅔ': 'ㅞ', 'ㅜㅣ': 'ㅟ', 'ㅡㅣ': 'ㅢ',
};
const FINAL_PAIRS: Record<string, string> = {
  'ㄱㅅ': 'ㄳ', 'ㄴㅈ': 'ㄵ', 'ㄴㅎ': 'ㄶ', 'ㄹㄱ': 'ㄺ', 'ㄹㅁ': 'ㄻ', 'ㄹㅂ': 'ㄼ',
  'ㄹㅅ': 'ㄽ', 'ㄹㅌ': 'ㄾ', 'ㄹㅍ': 'ㄿ', 'ㄹㅎ': 'ㅀ', 'ㅂㅅ': 'ㅄ',
};

const isVowel = (j: string) => JUNG.includes(j);
const isConsonant = (j: string) => CHO.includes(j);

interface Syllable { cho?: string; jung?: string; jong?: string }

/** Reads the typed jamo as one syllable; null when they do not make one. */
function parse(keys: string[]): Syllable | null {
  let i = 0;
  const s: Syllable = {};
  if (i < keys.length && isConsonant(keys[i])) s.cho = keys[i++];
  if (i < keys.length && isVowel(keys[i])) {
    s.jung = keys[i++];
    const pair = i < keys.length ? VOWEL_PAIRS[s.jung + keys[i]] : undefined;
    if (pair) { s.jung = pair; i++; }
  }
  if (s.cho && s.jung && i < keys.length && JONG.indexOf(keys[i]) > 0) {
    s.jong = keys[i++];
    const pair = i < keys.length ? FINAL_PAIRS[s.jong + keys[i]] : undefined;
    if (pair) { s.jong = pair; i++; }
  }
  if (!s.cho && !s.jung) return null;
  return i === keys.length ? s : null;
}

function render(s: Syllable | null): string {
  if (!s) return '';
  if (s.cho && s.jung) {
    const code = 0xac00 + (CHO.indexOf(s.cho) * 21 + JUNG.indexOf(s.jung)) * 28 + (s.jong ? JONG.indexOf(s.jong) : 0);
    return String.fromCharCode(code);
  }
  return s.cho ?? s.jung ?? '';
}

/** What a keystroke changed: text that is now final, and the syllable still being composed. */
export interface Composed { commit: string; preedit: string }

/**
 * Composes one syllable at a time from typed jamo. It keeps the keystrokes of
 * the syllable in progress, so Backspace takes back one jamo at a time (닭 →
 * 달 → 다 → ㄷ), as a desktop IME does.
 */
export class HangulComposer {
  private keys: string[] = [];

  get preedit(): string { return render(parse(this.keys)); }
  get active(): boolean { return this.keys.length > 0; }

  type(jamo: string): Composed {
    const next = [...this.keys, jamo];
    if (parse(next)) {
      this.keys = next;
      return { commit: '', preedit: this.preedit };
    }
    // A vowel after a final consonant takes that consonant as its initial: the
    // last key leaves this syllable (ㄺ keeps ㄹ, 각 gives up ㄱ).
    const last = this.keys[this.keys.length - 1];
    if (isVowel(jamo) && last && isConsonant(last) && this.keys.length >= 3) {
      const rest = parse(this.keys.slice(0, -1));
      if (rest?.jung) {
        this.keys = [last, jamo];
        return { commit: render(rest), preedit: this.preedit };
      }
    }
    const commit = this.preedit;
    this.keys = [jamo];
    return { commit, preedit: this.preedit };
  }

  /** Takes back the last jamo; handled is false when nothing was being composed. */
  backspace(): { handled: boolean; preedit: string } {
    if (!this.keys.length) return { handled: false, preedit: '' };
    this.keys = this.keys.slice(0, -1);
    return { handled: true, preedit: this.preedit };
  }

  /** Ends the syllable in progress and returns it. */
  flush(): string {
    const s = this.preedit;
    this.keys = [];
    return s;
  }

  reset(): void { this.keys = []; }
}
