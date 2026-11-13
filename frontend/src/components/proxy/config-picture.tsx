"use client"

import { createRef, useMemo, useRef, type RefObject } from "react"
import { FileText, FolderOpen } from "@/components/icons"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import { useMediaQuery } from "@/hooks/use-mobile"
import { ProductGlyph } from "@/components/product-logo"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireLink, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import {
  MAIN_NODE,
  relativePath,
  type IncludeFolder,
  type IncludeMap,
} from "@/components/proxy/config-tree"

type Tone = "logo" | "warning" | "danger"

function folderTone(node: IncludeFolder): Tone {
  if (node.broken > 0) return "danger"
  if (node.backups > 0) return "warning"
  return "logo"
}

/** What a folder holds, in the words its node and its phone line both use. */
function folderWords(node: IncludeFolder): string {
  const read =
    node.read === 0
      ? `${plural(node.files, "file")}, none read`
      : node.read === node.files
        ? `${plural(node.files, "file")} read`
        : `${node.read} of ${plural(node.files, "file")} read`
  const extra = [
    node.broken > 0 && `${node.broken} pointing at nothing`,
    node.backups > 0 && `${plural(node.backups, "backup")} read`,
    node.managed > 0 && `${node.managed} managed`,
  ].filter(Boolean)
  return [read, ...extra].join(" · ")
}

/**
 * The nginx directory as nginx walks it: the main file on the left, the
 * folders it includes in the next lane, the folders their files include in the
 * one after — sites-enabled's links into sites-available, a site into
 * snippets — and the folders it reads nothing in apart at the end, dashed. A
 * folder's line is red where a link in it points at nothing, amber where nginx
 * reads a backup there; it carries one pulse when the picture is drawn.
 *
 * Pressing a folder narrows the list under the picture to it; pressing the
 * main file opens it. Below `lg` the lanes are a list and each folder says in
 * words where nginx reaches it from.
 */
export function ConfigPicture({
  map,
  root,
  main,
  hint,
  onFolder,
  onOpenMain,
}: {
  map: IncludeMap
  root: string
  main: string
  /** The main file's line under its name: the last test, where there is one. */
  hint?: React.ReactNode
  onFolder: (folder: string) => void
  onOpenMain: () => void
}) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const container = useRef<HTMLDivElement>(null)
  const hub = useRef<HTMLDivElement>(null)
  const keys = map.reached.map((node) => node.folder).join("\n")
  const refs = useMemo(() => {
    const out = new Map<string, RefObject<HTMLDivElement | null>>()
    for (const key of keys.split("\n")) out.set(key, createRef<HTMLDivElement>())
    return out
  }, [keys])
  const refOf = (key: string) => (key === MAIN_NODE ? hub : refs.get(key))
  const mainName = relativePath(main, root)
  const label = (folder: string) => (folder === "" ? root : folder)

  const mainMark = (
    <WireLink label={`Open ${mainName}`} onClick={onOpenMain}>
      <WireMark tone="logo" size="md">
        <ProductGlyph id="nginx" />
      </WireMark>
    </WireLink>
  )

  if (!wide) {
    return (
      <ol aria-label="What nginx reads" className="flex min-w-0 flex-col gap-3 py-2">
        <li className="min-w-0">
          <WireNode
            mark={mainMark}
            title={<span className="font-mono">{mainName}</span>}
            hint={hint ?? "the main file"}
          />
        </li>
        {[...map.reached, ...map.unread].map((node) => {
          const from = node.from
            .map((parent) => (parent === MAIN_NODE ? mainName : label(parent)))
            .join(", ")
          return (
            <li key={node.folder} className="min-w-0">
              <WireNode
                mark={<FolderMark node={node} onFolder={onFolder} label={label(node.folder)} />}
                title={<span className="font-mono">{label(node.folder)}</span>}
                hint={
                  <span className="block truncate">
                    {node.read > 0 ? `← ${from} · ` : ""}
                    {folderWords(node)}
                  </span>
                }
              />
            </li>
          )
        })}
      </ol>
    )
  }

  const lanes = Array.from({ length: map.depth }, (_, index) =>
    map.reached.filter((node) => node.depth === index + 1),
  )
  const columns = [
    "auto",
    ...lanes.flatMap(() => ["minmax(2.5rem,0.6fr)", "auto"]),
    ...(map.unread.length > 0 ? ["minmax(2rem,0.4fr)", "auto"] : []),
  ].join(" ")

  return (
    <div ref={container} className="relative mx-auto max-w-5xl py-4">
      {map.reached.flatMap((node) =>
        node.from.map((parent) => {
          const from = refOf(parent)
          const to = refs.get(node.folder)
          if (!from || !to) return null
          const tone = folderTone(node)
          return (
            <AnimatedBeam
              key={`${parent}>${node.folder}`}
              containerRef={container}
              fromRef={from}
              toRef={to}
              shape="s"
              once
              tone={tone === "logo" ? "default" : tone}
              dashed={tone === "danger"}
              duration={2.4}
              delay={node.depth * 0.3}
            />
          )
        }),
      )}
      <ol
        aria-label="What nginx reads"
        className="grid min-h-28 items-center"
        style={{ gridTemplateColumns: columns }}
      >
        <li className="min-w-0">
          <WireNode
            nodeRef={hub}
            align="end"
            eyebrow="Main file"
            mark={mainMark}
            title={<span className="font-mono">{mainName}</span>}
            hint={hint}
          />
        </li>
        {lanes.map((lane, index) => {
          const last = index === lanes.length - 1
          return [
            <li key={`gap-${index}`} aria-hidden />,
            <li key={`lane-${index}`} className="min-w-0">
              <ul
                className={cn(
                  "flex flex-col justify-center",
                  // Room under each mark for the words that hang beneath it.
                  last ? "gap-4" : "gap-16 pb-10",
                )}
              >
                {lane.map((node) => (
                  <li key={node.folder} className="min-w-0">
                    <WireNode
                      nodeRef={refs.get(node.folder)}
                      align={last ? "start" : "center"}
                      mark={
                        <FolderMark node={node} onFolder={onFolder} label={label(node.folder)} />
                      }
                      title={<span className="font-mono">{label(node.folder)}</span>}
                      hint={folderWords(node)}
                    />
                  </li>
                ))}
              </ul>
            </li>,
          ]
        })}
        {map.unread.length > 0 && (
          <>
            <li aria-hidden />
            <li className="min-w-0">
              <p className="eyebrow mb-3">Not read</p>
              <ul className="flex flex-col gap-3">
                {map.unread.map((node) => (
                  <li key={node.folder} className="min-w-0">
                    <WireNode
                      mark={
                        <FolderMark node={node} onFolder={onFolder} label={label(node.folder)} />
                      }
                      title={
                        <span className="font-mono text-muted-foreground">
                          {label(node.folder)}
                        </span>
                      }
                      hint={plural(node.files, "file")}
                    />
                  </li>
                ))}
              </ul>
            </li>
          </>
        )}
      </ol>
    </div>
  )
}

function FolderMark({
  node,
  label,
  onFolder,
}: {
  node: IncludeFolder
  label: string
  onFolder: (folder: string) => void
}) {
  const Glyph = node.folder === "" ? FileText : FolderOpen
  return (
    <WireLink label={`Show the files in ${label}`} onClick={() => onFolder(node.folder)}>
      {node.read === 0 ? (
        // A dashed ring with the glyph alone: a fallback would add the
        // placeholder's +, which says "add this", not "nginx skips it".
        <WirePlaceholder size="md">
          <Glyph aria-hidden />
        </WirePlaceholder>
      ) : (
        <WireMark tone={folderTone(node)} size="md">
          <Glyph aria-hidden />
        </WireMark>
      )}
    </WireLink>
  )
}
