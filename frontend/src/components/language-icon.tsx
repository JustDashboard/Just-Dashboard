"use client"

import { languageProduct } from "@/lib/git-languages"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"

/**
 * The language a repository is mostly written in, as its logo and its name.
 *
 * A repository list is scanned, not read: the language is the second thing
 * that decides which of forty rows is the one, after its name, and as four
 * tracked-out capitals at 10px it was a word to read rather than a mark to
 * find. The logo is wayfinding, which is the exception §14 keeps — the same
 * rule that leaves the sidebar its icons.
 *
 * The mark is the language's own artwork in its own colours (`ProductGlyph`),
 * the one a repository's tile already carries (`git/languages.tsx`), so a
 * language looks the same on a card and in a picker. It used to be a
 * single-colour Material Design drawing tinted with Linguist's hue: a second
 * icon source for marks the bundle already had as the real thing.
 *
 * It does not *replace* the word. §4 says a fixed property of a row is a
 * `Tag`, and a tag is text; the logo is added in front of it, so a language
 * with no logo is still named rather than silently unmarked. That is why
 * there is no fallback glyph here: a generic code icon in front of "ELIXIR"
 * says nothing the word does not, and a column of identical generic marks is
 * worse than a column with gaps in it.
 *
 * GitHub reports the language as its own display name ("TypeScript", "C++"),
 * so the lookup is on a lowercased key: Linguist's names through
 * `languageProduct`, then the name itself where it is a product's id
 * ("markdown"), with Go's long name as Go.
 *
 * Drawn as a `Tag`'s contents rather than as a tag of its own, so it keeps
 * §4's shape: small caps text, no border, no ground — with a mark in front of
 * it where there is one.
 */
export function LanguageMark({ language }: { language?: string }) {
  if (!language) return null
  const key = language.trim().toLowerCase()
  const name = key === "golang" ? "go" : key
  const product = languageProduct(name) ?? name
  return (
    <span className="inline-flex min-w-0 items-center gap-1">
      {hasProductLogo(product) && <ProductGlyph id={product} />}
      <span className="truncate">{language}</span>
    </span>
  )
}
