"use client"

import { useEffect, useState } from "react"
import { loader } from "@monaco-editor/react"
import { CODE, CodeBlock } from "@/components/code"
import { MONACO_VS } from "@/lib/vendored"
import { tokenTone, type TokenTone } from "@/components/files/text-view"

type Monaco = typeof import("monaco-editor")

/**
 * A fenced block in a Markdown file, coloured by the editor's own grammar.
 *
 * Files already opens these files in Monaco, which knows some eighty
 * languages; its tokenizer reads a block here the way the editor would colour
 * it, with nothing new to ship and nothing to keep in step. It loads from this
 * origin, as the editor does (`code-editor.tsx`), and only once a file with a
 * fenced block is read: until it has, and for a language it does not know,
 * the block is its plain text, which is never wrong.
 */

const HUE: Record<TokenTone, string> = {
  keyword: CODE.keyword,
  string: CODE.string,
  comment: CODE.comment,
  number: CODE.number,
  type: CODE.literal,
  tag: CODE.key,
  attribute: CODE.key,
}

/** Past this a block is drawn plain: the head it came from is at most 64 KiB anyway. */
const MAX_HIGHLIGHT_CHARS = 64 << 10

let ready: Promise<Monaco> | undefined

function loadMonaco(): Promise<Monaco> {
  if (!ready) {
    loader.config({ paths: { vs: `${window.location.origin}${MONACO_VS}` } })
    ready = loader.init() as Promise<Monaco>
  }
  return ready
}

/** The fence's word (`sh`, `yml`, `TypeScript`) as one of Monaco's language ids. */
function languageId(monaco: Monaco, lang: string): string | undefined {
  const word = lang.trim().toLowerCase()
  if (!word) return undefined
  const alias: Record<string, string> = {
    sh: "shell",
    bash: "shell",
    zsh: "shell",
    console: "shell",
  }
  const wanted = alias[word] ?? word
  const known = monaco.languages.getLanguages()
  return known.find(
    (l) =>
      l.id === wanted ||
      l.aliases?.some((a) => a.toLowerCase() === wanted) ||
      l.extensions?.includes(`.${wanted}`),
  )?.id
}

type Piece = { text: string; tone: TokenTone | null }

async function highlight(lang: string, text: string): Promise<Piece[][] | null> {
  const monaco = await loadMonaco()
  const id = languageId(monaco, lang)
  if (!id) return null
  // colorize waits for the language's grammar to load; tokenize does not.
  await monaco.editor.colorize("", id, {})
  const lines = text.split("\n")
  return monaco.editor.tokenize(text, id).map((tokens, row) => {
    const line = lines[row] ?? ""
    return tokens.map((token, i) => ({
      text: line.slice(token.offset, tokens[i + 1]?.offset ?? line.length),
      tone: tokenTone(token.type),
    }))
  })
}

export function HighlightedCode({ lang, text }: { lang: string; text: string }) {
  const [pieces, setPieces] = useState<{ text: string; lines: Piece[][] } | null>(null)

  useEffect(() => {
    if (!lang || text.length > MAX_HIGHLIGHT_CHARS) return
    let live = true
    highlight(lang, text)
      .then((lines) => live && lines && setPieces({ text, lines }))
      .catch(() => undefined)
    return () => {
      live = false
    }
  }, [lang, text])

  const lines = pieces?.text === text ? pieces.lines : null
  return (
    <CodeBlock title={lang || "Code"} copy={text} className="my-1">
      {lines
        ? lines.map((line, row) => (
            <span key={row} data-highlighted={row === 0 ? "" : undefined}>
              {line.map((piece, i) =>
                piece.tone ? (
                  <span key={i} className={HUE[piece.tone]}>
                    {piece.text}
                  </span>
                ) : (
                  piece.text
                ),
              )}
              {row < lines.length - 1 && "\n"}
            </span>
          ))
        : text}
    </CodeBlock>
  )
}
