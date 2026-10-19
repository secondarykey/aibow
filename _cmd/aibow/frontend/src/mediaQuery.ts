// CSS のメディアクエリ(画面の幅)を React から見る。変わったら描き直す(ライト / ダークは theme.ts)
import { useEffect, useState } from "react";

// 狭いウィンドウ。会話の一覧を会話の上に重ねて出す幅(App.tsx)
export const NARROW_QUERY = "(max-width: 720px)";

export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const m = window.matchMedia(query);
    const onChange = () => setMatches(m.matches);
    onChange(); // query が変わったときのため
    m.addEventListener("change", onChange);
    return () => m.removeEventListener("change", onChange);
  }, [query]);
  return matches;
}
