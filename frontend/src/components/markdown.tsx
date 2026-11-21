"use client"

import { Fragment, useMemo } from "react"
import { Check, ChevronRight } from "@/components/icons"
import {
  markdownMedia,
  onlyMedia,
  parseMarkdown,
  safeHref,
  type Block,
  type Inline,
} from "@/lib/markdown"
import { cn } from "@/lib/utils"
import { MediaGrid, MediaImage, MediaLightbox } from "@/components/markdown-media"
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
  code,
  files,
  className,
}: {
  source: string
  /** The repository's page on its forge, e.g. `https://github.com/owner/name`. */
  repoUrl?: string
  /**
   * Draws a fenced block in place of the plain well, for a page whose code
   * blocks are things to act on: the SQL editor's assistant gives each
   * statement it wrote the verbs that open it in the editor.
   */
  code?: (block: { lang: string; v: string }) => React.ReactNode
  /**
   * A file on this server, read in Files: a reference with no scheme is
   * another file beside it, and nothing is fetched from anywhere else.
   */
  files?: MarkdownFiles
  className?: string
}) {
  const relative = files !== undefined
  const blocks = useMemo(() => parseMarkdown(source, { relative }), [source, relative])
  const media = useMemo(() => (relative ? [] : markdownMedia(blocks)), [blocks, relative])
  const forge = repoUrl ? new URL(repoUrl).origin : undefined
  return (
    <MediaLightbox media={media}>
      <div className={cn("min-w-0 space-y-3 text-body leading-relaxed break-words", className)}>
        <Blocks blocks={blocks} repoUrl={repoUrl} forge={forge} code={code} files={files} />
      </div>
    </MediaLightbox>
  )
}

/**
 * How a file's Markdown reaches the files beside it. `href` is the page's own
 * address for a reference (for a middle click), `open` is a plain click, and
 * `image` is a picture's address through `GET /files/raw`, or nothing for a
 * name that route would refuse.
 */
export type MarkdownFiles = {
  href: (ref: string) => string | undefined
  open: (ref: string) => void
  image: (ref: string) => string | undefined
}

type Links = {
  repoUrl?: string
  forge?: string
  code?: (block: { lang: string; v: string }) => React.ReactNode
  files?: MarkdownFiles
}

// Each rung opens more room above it than it leaves below, so a heading reads
// as the start of what follows rather than the end of what came before.
const HEADING = [
  "pt-4 text-base font-semibold",
  "pt-4 text-title font-semibold",
  "pt-2 text-sm font-semibold",
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
        <Tag className={cn(HEADING[block.level - 1], "leading-snug first:pt-0")}>
          <Runs runs={block.c} {...links} />
        </Tag>
      )
    }
    case "paragraph":
      if (onlyMedia(block.c)) {
        const pictures = block.c.filter((run) => run.t === "image")
        return (
          <MediaGrid count={pictures.length}>
            {pictures.map((run, index) =>
              links.files ? (
                <FilePicture key={index} src={run.src} alt={run.alt} files={links.files} />
              ) : (
                <MediaImage key={index} src={run.src} alt={run.alt} fill={pictures.length > 1} />
              ),
            )}
          </MediaGrid>
        )
      }
      return (
        <p className="text-foreground">
          <Runs runs={block.c} {...links} />
        </p>
      )
    case "code":
      if (links.code) return links.code(block)
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
    case "details":
      // The native element, so a closed fold's pictures are not fetched and
      // find-in-page still opens it.
      return (
        <details open={block.open} className="group/fold rounded-lg border border-hairline">
          <summary className="flex min-h-9 cursor-pointer list-none items-center gap-1.5 rounded-lg px-2.5 py-1.5 font-medium focus-ring-inset select-none hover:bg-row-hover [&::-webkit-details-marker]:hidden">
            <ChevronRight
              aria-hidden
              className="size-3.5 shrink-0 text-muted-foreground transition-transform group-open/fold:rotate-90"
            />
            <span className="min-w-0">
              <Runs runs={block.summary} {...links} />
            </span>
          </summary>
          <div className="space-y-3 border-t border-hairline px-3 py-3">
            <Blocks blocks={block.c} {...links} />
          </div>
        </details>
      )
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
                      ? "border-pull-merged bg-pull-merged text-background"
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

/**
 * A picture in a file on this server. One beside the file is drawn through the
 * raw route and its allowlist; one from another site is named and left out,
 * because opening a README must not make the browser call anywhere.
 */
function FilePicture({ src, alt, files }: { src: string; alt: string; files: MarkdownFiles }) {
  const remote = safeHref(src)
  const local = remote ? undefined : files.image(src)
  if (local) {
    return (
      // eslint-disable-next-line @next/next/no-img-element -- our own raw route
      <img
        src={local}
        alt={alt}
        loading="lazy"
        className="inline-block h-auto max-h-[32rem] max-w-full rounded-md align-top"
      />
    )
  }
  let where = src
  try {
    if (remote) where = new URL(remote).host
  } catch {}
  return (
    <span data-left-out className="text-hint text-muted-foreground">
      [{remote ? `Picture from ${where} not loaded` : `Picture ${src} not shown`}
      {alt ? `: ${alt}` : ""}]
    </span>
  )
}

function Runs({ runs, ...links }: { runs: Inline[] } & Links) {
  const { repoUrl, forge, files } = links
  return runs.map((run, index) => {
    switch (run.t) {
      case "text":
        return <Fragment key={index}>{run.v}</Fragment>
      case "image":
        return files ? (
          <FilePicture key={index} src={run.src} alt={run.alt} files={files} />
        ) : (
          <MediaImage key={index} src={run.src} alt={run.alt} />
        )
      case "br":
        return <br key={index} />
      case "code":
        return (
          <code
            key={index}
            className="rounded-sm border border-hairline bg-surface-sunken px-1 code-inline text-foreground"
          >
            {run.v}
          </code>
        )
      case "strong":
        return (
          <strong key={index} className="font-semibold text-foreground">
            <Runs runs={run.c} {...links} />
          </strong>
        )
      case "em":
        return (
          <em key={index}>
            <Runs runs={run.c} {...links} />
          </em>
        )
      case "del":
        return (
          <del key={index} className="text-muted-foreground">
            <Runs runs={run.c} {...links} />
          </del>
        )
      case "link":
        if (files && !safeHref(run.href)) {
          return (
            <a
              key={index}
              href={files.href(run.href)}
              onClick={(event) => {
                if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey) return
                event.preventDefault()
                files.open(run.href)
              }}
              className={LINK}
            >
              <Runs runs={run.c} {...links} />
            </a>
          )
        }
        return (
          <a key={index} href={run.href} target="_blank" rel="noreferrer noopener" className={LINK}>
            <Runs runs={run.c} {...links} />
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
