"use client"

import { useMemo } from "react"
import { cn } from "@/lib/utils"
import { Well } from "@/components/panel"
import { readDockerfile, type DockerfileKind } from "@/components/deploy/settings/dockerfile"

/**
 * Hues follow the log console's rule: the `--tag-*` set, one lightness, so no
 * kind of word outshouts another, and never the status hues, which are
 * readings of state. What the eye should skip is muted.
 */
const KIND: Record<DockerfileKind, string> = {
  instruction: "font-semibold text-[var(--tag-blue)]",
  comment: "italic text-muted-foreground/70",
  string: "text-[var(--tag-green)]",
  variable: "text-[var(--tag-amber)]",
  flag: "text-[var(--tag-cyan)]",
  stage: "font-medium text-[var(--tag-violet)]",
  image: "font-medium text-[var(--tag-pink)]",
  plain: "",
}

/** The facts the disclosure carries shut: size, stages, what they start from. */
export function dockerfileFacts(source: string) {
  const { facts } = readDockerfile(source)
  return [
    `${facts.lines} lines`,
    facts.stages > 1 && `${facts.stages} stages`,
    facts.bases.length > 0 && facts.bases.join(" · "),
  ]
    .filter(Boolean)
    .join(" · ")
}

/**
 * A Dockerfile the recipe wrote, drawn by its shapes with a line gutter.
 * A new build stage is ruled off from the one above, so a multi-stage file
 * reads as the stages it is.
 */
export function DockerfileView({ source }: { source: string }) {
  const { lines } = useMemo(() => readDockerfile(source), [source])
  const gutter = String(lines.length).length
  return (
    <Well className="max-h-72 px-0">
      <ol className="min-w-max">
        {lines.map((line, index) => (
          <li
            key={line.number}
            className={cn(
              "flex pr-3 whitespace-pre",
              line.fromStage !== undefined && index > 0 && "mt-1.5 border-t border-hairline pt-1.5",
            )}
          >
            <span
              aria-hidden
              className="numeric shrink-0 pr-3 text-right text-muted-foreground/40 select-none"
              style={{ width: `${gutter + 1.5}ch`, boxSizing: "content-box" }}
            >
              {line.number}
            </span>
            <span>
              {line.pieces.map((piece, at) => (
                <span key={at} className={KIND[piece.kind]}>
                  {piece.text}
                </span>
              ))}
              {line.pieces.length === 0 && " "}
            </span>
          </li>
        ))}
      </ol>
    </Well>
  )
}
