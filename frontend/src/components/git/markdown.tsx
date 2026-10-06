"use client"

import { Fragment, useMemo } from "react"
import { Check } from "@/components/icons"
import { parseMarkdown, type Block, type Inline } from "@/lib/markdown"
import { cn } from "@/lib/utils"
import { Well } from "@/components/panel"

/**
 * A pull request's description or a comment, drawn as the Markdown it was
 * written in (`lib/markdown.ts` says what is read and why nothing in it can
 * become markup).
 *
 * The type stays on this product's ladder rather than GitHub's: body text at
 * `text-body`, a heading a rung or two above it in semibold, code on the
 * well's recessed ground. A `#123` reference and an `@mention` link to the
 * forge when the caller says which repository the text belongs to — that is
 * where they point on the site the text was written on.
 */
export function Markdown({
  source,
  repoUrl,
  className,
}: {
  source: string
  /** The repository's page on its forge, e.g. `https://github.com/owner/name`. */
  repoUrl?: string
  className?: string
}) {
  const blocks = useMemo(() => parseMarkdown(source), [source])
  const forge = repoUrl ? new URL(repoUrl).origin : undefined
  return (
    <div className={cn("min-w-0 space-y-2.5 text-body leading-relaxed break-words", className)}>
      <Blocks blocks={blocks} repoUrl={repoUrl} forge={forge} />
    </div>
  )
}

type Links = { repoUrl?: string; forge?: string }

const HEADING = [
  "text-title font-semibold",
  "text-title font-semibold",
  "text-sm font-semibold",
  "text-body font-semibold",
  "text-body font-semibold",
  "text-body font-semibold text-muted-foreground",
]

function Blocks({ blocks, ...links }: { blocks: Block[] } & Links) {
  return blocks.map((block, index) => <BlockView key={index} block={block} {...links} />)
}

function BlockView({ block, ...links }: { block: Block } & Links) {
  switch (block.t) {
    case "heading": {
      const Tag = `h${Math.min(block.level + 2, 6)}` as "h3"
      return (
        <Tag
          className={cn(
            HEADING[block.level - 1],
            "pt-1 first:pt-0",
            block.level <= 2 && "border-b border-hairline pb-1",
          )}
        >
          <Runs runs={block.c} {...links} />
        </Tag>
      )
    }
    case "paragraph":
      return (
        <p className="text-foreground/90">
          <Runs runs={block.c} {...links} />
        </p>
      )
    case "code":
      return (
        <Well className="max-h-96 p-2.5 text-hint whitespace-pre" aria-label={block.lang || "code"}>
          {block.v}
        </Well>
      )
    case "quote":
      return (
        <blockquote className="space-y-2 border-l-2 border-border pl-3 text-muted-foreground">
          <Blocks blocks={block.c} {...links} />
        </blockquote>
      )
    case "rule":
      return <hr className="border-hairline" />
    case "list": {
      const List = block.ordered ? "ol" : "ul"
      const tasks = block.items.some((item) => item.task)
      return (
        <List
          start={block.ordered && block.start !== 1 ? block.start : undefined}
          className={cn(
            "space-y-1 pl-5",
            tasks ? "list-none pl-1" : block.ordered ? "list-decimal" : "list-disc",
            "marker:text-muted-foreground",
          )}
        >
          {block.items.map((item, index) => (
            <li key={index} className={cn(item.task && "flex items-start gap-2")}>
              {item.task && (
                <span
                  role="img"
                  aria-label={item.done ? "done" : "not done"}
                  className={cn(
                    "mt-[3px] flex size-3.5 shrink-0 items-center justify-center rounded-sm border",
                    item.done
                      ? "border-(--pull-merged) bg-(--pull-merged) text-background"
                      : "border-border",
                  )}
                >
                  {item.done && <Check className="size-2.5" strokeWidth={3} />}
                </span>
              )}
              <div
                className={cn(
                  "min-w-0 space-y-1.5",
                  item.task && item.done && "text-muted-foreground",
                )}
              >
                <Blocks blocks={item.c} {...links} />
              </div>
            </li>
          ))}
        </List>
      )
    }
    case "table":
      return (
        <div className="overflow-x-auto rounded-lg border border-hairline">
          <table className="w-full text-hint">
            <thead className="bg-surface-header">
              <tr>
                {block.head.map((cell, index) => (
                  <th
                    key={index}
                    style={{ textAlign: block.align[index] }}
                    className="border-b border-hairline px-2.5 py-1.5 text-left font-medium"
                  >
                    <Runs runs={cell} {...links} />
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-hairline">
              {block.rows.map((row, r) => (
                <tr key={r}>
                  {row.map((cell, index) => (
                    <td
                      key={index}
                      style={{ textAlign: block.align[index] }}
                      className="px-2.5 py-1.5 align-top"
                    >
                      <Runs runs={cell} {...links} />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )
  }
}

const LINK =
  "text-foreground underline decoration-muted-foreground/60 underline-offset-2 hover:decoration-foreground"

function Runs({ runs, repoUrl, forge }: { runs: Inline[] } & Links) {
  return runs.map((run, index) => {
    switch (run.t) {
      case "text":
        return <Fragment key={index}>{run.v}</Fragment>
      case "br":
        return <br key={index} />
      case "code":
        return (
          <code
            key={index}
            className="rounded-sm border border-hairline bg-surface-sunken px-1 font-mono text-[0.9em] text-foreground"
          >
            {run.v}
          </code>
        )
      case "strong":
        return (
          <strong key={index} className="font-semibold text-foreground">
            <Runs runs={run.c} repoUrl={repoUrl} forge={forge} />
          </strong>
        )
      case "em":
        return (
          <em key={index}>
            <Runs runs={run.c} repoUrl={repoUrl} forge={forge} />
          </em>
        )
      case "del":
        return (
          <del key={index} className="text-muted-foreground">
            <Runs runs={run.c} repoUrl={repoUrl} forge={forge} />
          </del>
        )
      case "link":
        return (
          <a key={index} href={run.href} target="_blank" rel="noreferrer noopener" className={LINK}>
            <Runs runs={run.c} repoUrl={repoUrl} forge={forge} />
          </a>
        )
      case "ref":
        return repoUrl ? (
          <a
            key={index}
            href={`${repoUrl}/issues/${run.n}`}
            target="_blank"
            rel="noreferrer noopener"
            className="numeric font-medium text-foreground hover:underline"
          >
            #{run.n}
          </a>
        ) : (
          <span key={index} className="numeric font-medium">
            #{run.n}
          </span>
        )
      case "mention":
        return forge ? (
          <a
            key={index}
            href={`${forge}/${run.v}`}
            target="_blank"
            rel="noreferrer noopener"
            className="font-semibold text-foreground hover:underline"
          >
            @{run.v}
          </a>
        ) : (
          <span key={index} className="font-semibold">
            @{run.v}
          </span>
        )
    }
  })
}
