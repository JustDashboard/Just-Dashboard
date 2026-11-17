"use client"

import { useMemo } from "react"
import { Well } from "@/components/panel"
import { CodeLines, DockerfilePieces, dockerfileLines } from "@/components/code"
import { readDockerfile } from "@/components/deploy/settings/dockerfile"

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
 * A Dockerfile the recipe wrote, in the code palette the run page uses, with
 * the shared line gutter.
 * A new build stage is ruled off from the one above, so a multi-stage file
 * reads as the stages it is.
 */
export function DockerfileView({ source }: { source: string }) {
  const lines = useMemo(() => dockerfileLines(source), [source])
  return (
    <Well className="max-h-72 px-0">
      <CodeLines
        lines={lines.map((line) => line.pieces.map((piece) => piece.text).join(""))}
        wrap={false}
        rule={(index) => index > 0 && lines[index]?.fromStage !== undefined}
        render={(_line, index) => <DockerfilePieces pieces={lines[index]?.pieces ?? []} />}
        className="pr-3"
      />
    </Well>
  )
}
