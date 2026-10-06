/**
 * What a checkout is written in, as the server measured it (`languages` on a
 * repository summary: the share of tracked bytes per language, largest first),
 * and how each language is drawn.
 *
 * The names are the backend's fixed set, which is Linguist's spelling — the
 * one a repository already carries on GitHub — so a checkout and its page on
 * the forge agree. Each has a colour token (`--language-*`, Linguist's hue at
 * the product's lightness, see `globals.css`) and, where the product bundles
 * one, the language's own logo. A name outside the set keeps the slate tag
 * hue and no logo rather than borrowing a neighbour's.
 */

export type RepoLanguage = { name: string; share: number }

const LANGUAGES: Record<string, { token: string; product?: string }> = {
  typescript: { token: "typescript", product: "typescript" },
  javascript: { token: "javascript", product: "javascript" },
  go: { token: "go", product: "go" },
  python: { token: "python", product: "python" },
  rust: { token: "rust", product: "rust" },
  java: { token: "java", product: "java" },
  kotlin: { token: "kotlin", product: "kotlin" },
  ruby: { token: "ruby", product: "ruby" },
  php: { token: "php", product: "php" },
  swift: { token: "swift", product: "swift" },
  c: { token: "c", product: "c" },
  "c++": { token: "cpp", product: "cplusplus" },
  "c#": { token: "csharp", product: "csharp" },
  haskell: { token: "haskell", product: "haskell" },
  lua: { token: "lua", product: "lua" },
  r: { token: "r", product: "r" },
  html: { token: "html", product: "html5" },
  css: { token: "css", product: "css3" },
  scss: { token: "scss", product: "sass" },
  shell: { token: "shell", product: "shellscript" },
  vue: { token: "vue", product: "vuejs" },
  svelte: { token: "svelte", product: "svelte" },
  dart: { token: "dart", product: "dart" },
  elixir: { token: "elixir", product: "elixir" },
  scala: { token: "scala", product: "scala" },
  zig: { token: "zig", product: "zig" },
  perl: { token: "perl", product: "perl" },
  dockerfile: { token: "dockerfile", product: "docker" },
  powershell: { token: "powershell", product: "powershell" },
}

const entry = (name: string) => LANGUAGES[name.trim().toLowerCase()]

/** The colour a language is drawn in, as a CSS value. */
export function languageColour(name: string): string {
  const known = entry(name)
  return known ? `var(--language-${known.token})` : "var(--tag-slate)"
}

/** The product logo a language is drawn as, when one is bundled. */
export function languageProduct(name: string): string | undefined {
  return entry(name)?.product
}

/** A share as the forge prints it: one decimal under ten per cent, whole above. */
export function languagePercent(share: number): string {
  const percent = share * 100
  if (percent > 0 && percent < 0.1) return "<0.1%"
  return `${percent < 10 ? percent.toFixed(1) : Math.round(percent)}%`
}

/**
 * The segments of a language bar: every language the summary named, in its
 * order, with what is left of the whole as one "other" segment so the bar
 * always spans its track — the server drops languages under one per cent.
 */
export function languageSegments(
  languages: RepoLanguage[] | undefined,
): { name: string; share: number; colour: string }[] {
  const named = (languages ?? []).filter((l) => l.share > 0)
  const segments = named.map((l) => ({ ...l, colour: languageColour(l.name) }))
  const rest = 1 - named.reduce((sum, l) => sum + l.share, 0)
  if (named.length > 0 && rest > 0.005) {
    segments.push({ name: "Other", share: rest, colour: "var(--meter-track)" })
  }
  return segments
}
