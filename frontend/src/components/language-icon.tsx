"use client"

import {
  mdiLanguageC,
  mdiLanguageCpp,
  mdiLanguageCsharp,
  mdiLanguageCss3,
  mdiLanguageGo,
  mdiLanguageHaskell,
  mdiLanguageHtml5,
  mdiLanguageJava,
  mdiLanguageJavascript,
  mdiLanguageKotlin,
  mdiLanguageLua,
  mdiLanguageMarkdown,
  mdiLanguagePhp,
  mdiLanguagePython,
  mdiLanguageR,
  mdiLanguageRuby,
  mdiLanguageRust,
  mdiLanguageSwift,
  mdiLanguageTypescript,
} from "@mdi/js"

/**
 * The logo of the language a repository is mostly written in.
 *
 * A repository list is scanned, not read: the language is the second thing
 * that decides which of forty rows is the one, after its name, and as four
 * tracked-out capitals at 10px it was a word to read rather than a mark to
 * find. The glyph is wayfinding, which is the exception §14 keeps — the same
 * rule that leaves the sidebar its icons.
 *
 * It does not *replace* the word. §4 says a fixed property of a row is a
 * `Tag`, and a tag is text; the logo is added in front of it, so a language
 * MDI does not draw is still named rather than silently unmarked. That is why
 * there is no fallback glyph here: a generic code icon in front of "ELIXIR"
 * says nothing the word does not, and a column of identical generic marks is
 * worse than a column with gaps in it.
 *
 * GitHub reports the language as its own display name ("TypeScript", "C++"),
 * so the lookup is on a lowercased key and carries the aliases that differ
 * from it.
 *
 * **And the mark carries its own colour.** Drawn in `text-muted-foreground` it
 * was the same grey as the forty words around it, which is a mark doing the
 * half of its job that costs pixels and none of the half that saves a read: a
 * column of twenty identical grey glyphs is a texture, and the reader goes
 * back to reading the words the glyph was added to save them reading. The hue
 * is the one the same repository carries on the site the list came from
 * (GitHub Linguist's), the lightness is this product's, and both live in
 * `globals.css` as `--language-*` — §1 keeps every colour named in one place,
 * and the token is reached the way `files/file-icon.tsx` reaches its own.
 */
const LANGUAGE_MARKS: Record<string, { path: string; colour: string }> = {
  typescript: { path: mdiLanguageTypescript, colour: "var(--language-typescript)" },
  javascript: { path: mdiLanguageJavascript, colour: "var(--language-javascript)" },
  python: { path: mdiLanguagePython, colour: "var(--language-python)" },
  go: { path: mdiLanguageGo, colour: "var(--language-go)" },
  golang: { path: mdiLanguageGo, colour: "var(--language-go)" },
  rust: { path: mdiLanguageRust, colour: "var(--language-rust)" },
  java: { path: mdiLanguageJava, colour: "var(--language-java)" },
  kotlin: { path: mdiLanguageKotlin, colour: "var(--language-kotlin)" },
  ruby: { path: mdiLanguageRuby, colour: "var(--language-ruby)" },
  php: { path: mdiLanguagePhp, colour: "var(--language-php)" },
  swift: { path: mdiLanguageSwift, colour: "var(--language-swift)" },
  c: { path: mdiLanguageC, colour: "var(--language-c)" },
  "c++": { path: mdiLanguageCpp, colour: "var(--language-cpp)" },
  "c#": { path: mdiLanguageCsharp, colour: "var(--language-csharp)" },
  haskell: { path: mdiLanguageHaskell, colour: "var(--language-haskell)" },
  lua: { path: mdiLanguageLua, colour: "var(--language-lua)" },
  r: { path: mdiLanguageR, colour: "var(--language-r)" },
  html: { path: mdiLanguageHtml5, colour: "var(--language-html)" },
  css: { path: mdiLanguageCss3, colour: "var(--language-css)" },
  scss: { path: mdiLanguageCss3, colour: "var(--language-scss)" },
  markdown: { path: mdiLanguageMarkdown, colour: "var(--language-markdown)" },
}

/**
 * The language of a repository, as its logo and its name.
 *
 * Drawn as a `Tag`'s contents rather than as a tag of its own, so it keeps
 * §4's shape: small caps text, no border, no ground — with a mark in front of
 * it where there is one.
 */
export function LanguageMark({ language }: { language?: string }) {
  if (!language) return null
  // The path is drawn inline rather than wrapped in an `Icon` component. A
  // component built inside a render is a new type on every pass, which
  // remounts the glyph each time the repository list polls — and there is no
  // `Icon` behaviour wanted here beyond painting one path at one size.
  const mark = LANGUAGE_MARKS[language.trim().toLowerCase()]
  return (
    <span className="inline-flex min-w-0 items-center gap-1">
      {mark && (
        <svg
          aria-hidden
          viewBox="0 0 24 24"
          fill="currentColor"
          className="size-3.5 shrink-0"
          style={{ color: mark.colour }}
        >
          <path d={mark.path} />
        </svg>
      )}
      <span className="truncate">{language}</span>
    </span>
  )
}
