export type DockerfileKind =
  "instruction" | "comment" | "string" | "variable" | "flag" | "stage" | "image" | "plain"

export type DockerfilePiece = { kind: DockerfileKind; text: string }

export type DockerfileLine = {
  number: number
  pieces: DockerfilePiece[]
  /** The line opens a build stage, so the viewer can rule it off. */
  fromStage?: string
}

export type DockerfileFacts = {
  lines: number
  stages: number
  /** Base images in the order the stages name them, scratch and stage references left out. */
  bases: string[]
}

const INSTRUCTIONS = new Set([
  "ADD",
  "ARG",
  "CMD",
  "COPY",
  "ENTRYPOINT",
  "ENV",
  "EXPOSE",
  "FROM",
  "HEALTHCHECK",
  "LABEL",
  "MAINTAINER",
  "ONBUILD",
  "RUN",
  "SHELL",
  "STOPSIGNAL",
  "USER",
  "VOLUME",
  "WORKDIR",
])

const FROM_ARGS = /^((?:--\S+\s+)*)(\S+)(?:(\s+)([Aa][Ss])(\s+)(\S+))?/
const TOKEN =
  /(\$\{[^}]*\}|\$[A-Za-z_][A-Za-z0-9_]*)|("(?:[^"\\]|\\.)*"|'[^']*')|(\s--[a-z-]+(?==|\s|$))/g

function pushPlain(pieces: DockerfilePiece[], text: string) {
  if (text) pieces.push({ kind: "plain", text })
}

/** The arguments of one instruction: variables, quoted strings and `--flags`. */
function argumentPieces(rest: string): DockerfilePiece[] {
  const pieces: DockerfilePiece[] = []
  let last = 0
  for (const match of rest.matchAll(TOKEN)) {
    pushPlain(pieces, rest.slice(last, match.index))
    const kind: DockerfileKind = match[1] ? "variable" : match[2] ? "string" : "flag"
    pieces.push({ kind, text: match[0] })
    last = match.index + match[0].length
  }
  pushPlain(pieces, rest.slice(last))
  return pieces
}

/**
 * A Dockerfile as lines of coloured pieces, split by shape the way
 * `lib/log-tokens.ts` splits a log line. A line continued with a backslash
 * keeps the instruction it belongs to: its next line is arguments, not a new
 * instruction.
 */
export function readDockerfile(source: string): {
  lines: DockerfileLine[]
  facts: DockerfileFacts
} {
  const lines: DockerfileLine[] = []
  const bases: string[] = []
  const names = new Set<string>()
  let stages = 0
  let continued = false

  source.split("\n").forEach((text, index) => {
    const line: DockerfileLine = { number: index + 1, pieces: [] }
    lines.push(line)
    const trimmed = text.trimStart()
    if (trimmed.startsWith("#")) {
      line.pieces.push({ kind: "comment", text })
      return
    }
    const indent = text.slice(0, text.length - trimmed.length)
    const word = continued ? undefined : trimmed.match(/^[A-Za-z]+/)?.[0]
    const instruction = word && INSTRUCTIONS.has(word.toUpperCase()) ? word : undefined
    pushPlain(line.pieces, indent)
    let rest = trimmed
    if (instruction) {
      line.pieces.push({ kind: "instruction", text: instruction })
      rest = trimmed.slice(instruction.length)
    }
    if (instruction?.toUpperCase() === "FROM") {
      const spaces = rest.match(/^\s*/)![0]
      const parts = rest.slice(spaces.length).match(FROM_ARGS)
      if (parts) {
        const [whole, flags, image, gap, as, gap2, name] = parts
        stages += 1
        if (image !== "scratch" && !names.has(image)) bases.push(image)
        if (name) {
          names.add(name)
          line.fromStage = name
        }
        pushPlain(line.pieces, spaces)
        line.pieces.push(...argumentPieces(flags))
        line.pieces.push({ kind: "image", text: image })
        if (as) {
          line.pieces.push(
            { kind: "plain", text: gap },
            { kind: "instruction", text: as },
            { kind: "plain", text: gap2 },
            { kind: "stage", text: name },
          )
        }
        line.pieces.push(...argumentPieces(rest.slice(spaces.length + whole.length)))
        continued = text.trimEnd().endsWith("\\")
        return
      }
    }
    line.pieces.push(...argumentPieces(rest))
    continued = text.trimEnd().endsWith("\\")
  })

  return { lines, facts: { lines: lines.length, stages, bases } }
}
