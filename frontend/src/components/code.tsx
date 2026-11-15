"use client"

import { CopyAction } from "@/components/copy"
import { tokenClass } from "@/components/logs/log-text"
import {
  readDockerfile,
  type DockerfileKind,
  type DockerfileLine,
  type DockerfilePiece,
} from "@/components/deploy/settings/dockerfile"
import { plural, shortDigest } from "@/lib/format"
import { cn } from "@/lib/utils"

/*
 * Code as the product draws it: one palette, one well with its caption, one
 * gutter. A Dockerfile, a command, a record as JSON and a properties file
 * read in the same hues on every page, so a file a click apart does not
 * change colour scheme.
 */

/**
 * The hues a code block draws in: the `--tag-*` rung the log console's tokens
 * sit on, so no kind outshouts another, and none of the status hues — a
 * string is not a success. Keys and keywords take the blues and the violet,
 * values the green and the pink, paths the cyan the console gives a path,
 * and the punctuation steps back.
 */
export const CODE = {
  key: "text-tag-blue",
  keyword: "font-semibold text-tag-violet",
  literal: "text-tag-violet",
  string: "text-tag-green",
  number: "text-tag-pink",
  variable: "text-tag-pink",
  path: "text-tag-cyan",
  flag: "text-tag-cyan",
  digest: "text-tag-slate",
  comment: "text-faint italic",
  punct: "text-faint",
  program: "font-semibold text-foreground",
}

const PATH = /^(\/|\.\/|\.just-dashboard\/)/
const URL_TEXT = /^https?:\/\//
const DIGEST = /^sha256:[0-9a-f]{12,}$/
const HEX = /^[0-9a-f]{12,64}$/

/**
 * A Dockerfile's pieces in the code palette. The instruction is the keyword,
 * the image a stage starts from is a value (the thing the line names), and
 * the stage's own name is the violet a literal takes.
 */
export const DOCKERFILE_HUE: Record<DockerfileKind, string | undefined> = {
  instruction: CODE.keyword,
  comment: CODE.comment,
  string: CODE.string,
  variable: CODE.variable,
  flag: CODE.flag,
  stage: CODE.literal,
  image: CODE.string,
  plain: undefined,
}

/**
 * A whole Dockerfile as lines of pieces: the one tokeniser, which keeps a
 * line continued with a backslash in the instruction it belongs to.
 */
export function dockerfileLines(source: string): DockerfileLine[] {
  return readDockerfile(source.replace(/\n$/, "")).lines
}

/** One Dockerfile line on its own, as pieces. A continued line needs `dockerfileLines`. */
export function tokenDockerfile(line: string): DockerfilePiece[] {
  return readDockerfile(line).lines[0]?.pieces ?? []
}

/** A Dockerfile line's pieces, drawn in the code palette. */
export function DockerfilePieces({ pieces }: { pieces: DockerfilePiece[] }) {
  return (
    <>
      {pieces.map((piece, at) => (
        <span key={at} className={DOCKERFILE_HUE[piece.kind]}>
          {piece.text}
        </span>
      ))}
    </>
  )
}

/**
 * The gutter's width in `ch`: as many digits as the last line number has, so
 * a 9-line file and a 900-line one each take what they need.
 */
export function gutterCh(lines: number): number {
  return String(Math.max(1, lines)).length
}

/**
 * Lines with a number gutter, in the faint ink every gutter uses. `render`
 * draws a line's text (a highlighter); without it the line is plain.
 * `wrap` breaks a long line under itself instead of scrolling sideways.
 */
export function CodeLines({
  lines,
  render,
  wrap = true,
  className,
}: {
  lines: string[]
  render?: (line: string, index: number) => React.ReactNode
  wrap?: boolean
  className?: string
}) {
  const gutter = gutterCh(lines.length)
  return (
    <span className={cn("block", !wrap && "min-w-max", className)}>
      {lines.map((line, index) => (
        <span key={index} className="flex">
          <span
            aria-hidden
            data-slot="code-gutter"
            className="numeric shrink-0 pr-3 text-right text-faint select-none"
            style={{ width: `${gutter + 3}ch` }}
          >
            {index + 1}
          </span>
          <span
            className={cn("min-w-0", wrap ? "wrap-anywhere whitespace-pre-wrap" : "whitespace-pre")}
          >
            {render ? render(line, index) : line}
            {line === "" && " "}
          </span>
        </span>
      ))}
    </span>
  )
}

/**
 * Text kept as it was written, in a recessed block with its name, its length
 * and a copy. A `Well`'s ground with a caption strip, so a Dockerfile and the
 * record around it read as one thing and its name.
 */
export function CodeBlock({
  title,
  mark,
  copy,
  lines,
  className,
  children,
}: {
  title: string
  mark?: React.ReactNode
  copy: string
  lines?: number
  className?: string
  children: React.ReactNode
}) {
  return (
    <figure
      className={cn(
        "min-w-0 overflow-hidden rounded-lg border border-hairline bg-surface-sunken",
        className,
      )}
    >
      <figcaption className="flex min-h-8 min-w-0 items-center gap-2 border-b border-hairline px-3 py-1 text-hint text-muted-foreground">
        {mark}
        <span className="truncate font-medium text-foreground">{title}</span>
        {lines !== undefined && <span className="numeric">{plural(lines, "line")}</span>}
        <CopyAction text={copy} label={`Copy ${title.toLowerCase()}`} className="-my-1 ml-auto" />
      </figcaption>
      <pre className="max-h-96 overflow-auto p-3 font-mono text-xs leading-relaxed">
        <code>{children}</code>
      </pre>
    </figure>
  )
}

/**
 * An image or content digest as the product shows it everywhere: twelve hex
 * characters in mono (`shortDigest`), the whole of it on hover and a press
 * away.
 */
export function Digest({ value, label }: { value: string; label: string }) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1 font-mono text-xs" title={value}>
      <span className={tokenClass("id")}>{shortDigest(value)}</span>
      <CopyAction text={value} label={`Copy ${label.toLowerCase()}`} className="-my-1" />
    </span>
  )
}

const SHELL_WORD = /"(?:[^"\\]|\\.)*"|'[^']*'|&&|\|\||[|;]|\S+/g

/**
 * A command as it was typed — a build or start command, a check's request —
 * coloured the way a command the engine ran is coloured, inline in a line
 * that reads a plan back. The words are the reader's own, so nothing is quoted
 * again: the program after each `&&`, `|` or `;`, a flag, a path, a quoted
 * word and a variable each take their hue, and an assignment in front of the
 * program keeps its name and its value apart.
 */
export function ShellWords({ command, className }: { command: string; className?: string }) {
  const out: React.ReactNode[] = []
  let at = 0
  let program = true
  for (const match of command.matchAll(SHELL_WORD)) {
    const index = match.index ?? 0
    if (index > at) out.push(command.slice(at, index))
    const word = match[0]
    const operator = word === "&&" || word === "||" || word === "|" || word === ";"
    const assignment: boolean = program && /^[A-Za-z_][A-Za-z0-9_]*=/.test(word)
    out.push(
      assignment ? (
        <span key={index}>
          <span className={CODE.key}>{word.slice(0, word.indexOf("="))}</span>
          <span className={CODE.punct}>=</span>
          <span className={CODE.string}>{word.slice(word.indexOf("=") + 1)}</span>
        </span>
      ) : (
        <span key={index} className={shellWordHue(word, { operator, program })}>
          {word}
        </span>
      ),
    )
    program = operator || assignment
    at = index + word.length
  }
  if (at < command.length) out.push(command.slice(at))
  return <code className={cn("font-mono", className)}>{out}</code>
}

/** The hue of one word of a command line, by its place and its shape. */
export function shellWordHue(
  word: string,
  { operator = false, program = false }: { operator?: boolean; program?: boolean } = {},
): string | undefined {
  if (operator) return CODE.punct
  if (program) return CODE.program
  if (/^["']/.test(word)) return CODE.string
  if (word.startsWith("-")) return CODE.flag
  if (word.startsWith("$")) return CODE.variable
  if (PATH.test(word) || word === ".") return CODE.path
  if (/^\d+$/.test(word)) return CODE.number
  return undefined
}

const JSON_TOKEN =
  /("(?:[^"\\]|\\.)*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false|null)\b|([{}[\],])/g

/** A record as JSON, laid out two spaces a level and coloured by what each token is. */
export function JsonCode({ value }: { value: unknown }) {
  const source = JSON.stringify(value, null, 2) ?? ""
  const out: React.ReactNode[] = []
  let at = 0
  for (const match of source.matchAll(JSON_TOKEN)) {
    const index = match.index ?? 0
    if (index > at) out.push(source.slice(at, index))
    const [whole, string, colon, number, literal] = match
    if (string !== undefined && colon !== undefined) {
      out.push(
        <span key={index} className={CODE.key}>
          {string}
        </span>,
        <span key={`${index}:`} className={CODE.punct}>
          {colon}
        </span>,
      )
    } else if (string !== undefined) {
      const inner = string.slice(1, -1)
      out.push(
        <span
          key={index}
          className={
            PATH.test(inner) || URL_TEXT.test(inner)
              ? CODE.path
              : DIGEST.test(inner) || HEX.test(inner)
                ? CODE.digest
                : CODE.string
          }
        >
          {string}
        </span>,
      )
    } else if (number !== undefined) {
      out.push(
        <span key={index} className={CODE.number}>
          {number}
        </span>,
      )
    } else if (literal !== undefined) {
      out.push(
        <span key={index} className={CODE.literal}>
          {literal}
        </span>,
      )
    } else {
      out.push(
        <span key={index} className={CODE.punct}>
          {whole}
        </span>,
      )
    }
    at = index + whole.length
  }
  if (at < source.length) out.push(source.slice(at))
  return <span className="whitespace-pre">{out}</span>
}
