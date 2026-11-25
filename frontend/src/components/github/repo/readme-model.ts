import type { RepoLanguage } from "@/lib/git-languages"
import type { GitHubLanguages } from "@/lib/github-types"

/**
 * A README's references made whole, so the page can draw it the way GitHub
 * does without reading anything from the repository itself.
 *
 * A README points at files beside it (`docs/setup.md`, `./screenshot.png`)
 * by paths that mean something only on GitHub, against the README's own
 * folder at the branch it was read at. The Markdown renderer here follows no
 * relative reference (`lib/markdown.ts`), so each is rewritten before it is
 * parsed: a picture to GitHub's `raw` address, which the dashboard's forge
 * media reader fetches for the page, and a link to GitHub's `blob` page. A
 * reference that already has a scheme, a fragment and anything that is not a
 * plain path are left as they were written.
 */
export function absoluteReadme(
  source: string,
  fullName: string,
  ref: string,
  readmePath = "README.md",
): string {
  const base = `https://github.com/${fullName}`
  const folder = readmePath.includes("/") ? readmePath.slice(0, readmePath.lastIndexOf("/")) : ""
  const resolve = (path: string): string | undefined => {
    if (!path || path.startsWith("#") || /^[a-z][a-z0-9+.-]*:/i.test(path)) return undefined
    if (/^[\\/]{2}/.test(path) || /[\s\u0000-\u001f\u007f\\]/.test(path)) return undefined
    const [plain, rest = ""] = splitSuffix(path)
    const parts = plain.startsWith("/") ? [] : folder ? folder.split("/") : []
    for (const part of plain.split("/")) {
      if (!part || part === ".") continue
      if (part === "..") {
        if (parts.length === 0) return undefined
        parts.pop()
      } else parts.push(part)
    }
    if (parts.length === 0) return undefined
    return `${parts.map(encodeURIComponent).join("/")}${rest}`
  }
  const branch = encodeURIComponent(ref)
  return source.replace(
    /(!?)\[((?:[^\]\\]|\\.)*)\]\(\s*<?([^)\s>]+)>?((?:\s+"[^"]*")?)\s*\)/g,
    (whole, bang: string, text: string, target: string, title: string) => {
      const path = resolve(target)
      if (!path) return whole
      const kind = bang ? "raw" : "blob"
      return `${bang}[${text}](${base}/${kind}/${branch}/${path}${title})`
    },
  )
}

function splitSuffix(path: string): [string, string] {
  const at = path.search(/[?#]/)
  return at < 0 ? [path, ""] : [path.slice(0, at), path.slice(at)]
}

/**
 * GitHub's bytes per language as shares of the whole, largest first, for the
 * language bar Local Git draws a checkout with. A language under a tenth of a
 * per cent is left to the bar's "other" rest.
 */
export function languageShares(answer: GitHubLanguages | undefined): RepoLanguage[] {
  const languages = answer?.languages ?? []
  const total = languages.reduce((sum, language) => sum + language.bytes, 0)
  if (total <= 0) return []
  return languages
    .map((language) => ({ name: language.name, share: language.bytes / total }))
    .filter((language) => language.share >= 0.001)
    .sort((a, b) => b.share - a.share)
}
