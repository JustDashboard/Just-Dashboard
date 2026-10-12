"use client"

import { Fragment, memo, useMemo } from "react"
import { Check } from "@/components/icons"
import { parseMarkdown, type Block, type Inline, type ListItem } from "@/lib/markdown"
import { cn } from "@/lib/utils"

/**
 * A note's text, drawn as the Markdown it was written in.
 *
 * The same reader as a pull request's description (`lib/markdown.ts`, which
 * never produces markup), drawn for paper rather than for the product's
 * ground: every size is in `em`, so a note's S to XL scales its headings and
 * its boxes with it, and every colour is the note's own ink
 * (`[data-note-color]` in globals.css), so a list on yellow paper and the same
 * list on a graphite note each read as written on that note.
 *
 * A task's box is a real button while the board can be edited, and pressing it
 * ticks that task in the text — the note's count in its corner is the same
 * text read again. `onToggle` receives the task's index in reading order,
 * which `toggleTask` counts the same way.
 */
export const NoteMarkdown = memo(function NoteMarkdown({
  text,
  onToggle,
}: {
  text: string
  onToggle?: (index: number) => void
}) {
  const [blocks, tasks] = useMemo(() => {
    const parsed = parseMarkdown(text)
    return [parsed, indexTasks(parsed)] as const
  }, [text])
  return <Blocks blocks={blocks} tasks={tasks} onToggle={onToggle} />
})

/** Each task item's place among all of a note's tasks, in reading order. */
function indexTasks(blocks: Block[], into = new Map<ListItem, number>()) {
  for (const block of blocks) {
    if (block.t === "quote") indexTasks(block.c, into)
    if (block.t !== "list") continue
    for (const item of block.items) {
      if (item.task) into.set(item, into.size)
      indexTasks(item.c, into)
    }
  }
  return into
}

type Context = { tasks: Map<ListItem, number>; onToggle?: (index: number) => void }

const HEADING = ["text-[1.45em] font-bold", "text-[1.25em] font-bold", "text-[1.1em] font-semibold"]

function Blocks({ blocks, ...context }: { blocks: Block[] } & Context) {
  return blocks.map((block, index) => <BlockView key={index} block={block} {...context} />)
}

function BlockView({ block, ...context }: { block: Block } & Context) {
  switch (block.t) {
    case "heading":
      return (
        <p className={cn(HEADING[Math.min(block.level, 3) - 1], "leading-tight")}>
          <Runs runs={block.c} />
        </p>
      )
    case "paragraph":
      return (
        <p>
          <Runs runs={block.c} />
        </p>
      )
    case "code":
      return (
        <pre className="overflow-hidden rounded-md bg-(--note-wash) px-[0.5em] py-[0.35em] font-mono text-[0.85em] whitespace-pre-wrap">
          {block.v}
        </pre>
      )
    case "quote":
      return (
        <blockquote className="space-y-[0.35em] border-l-2 border-(--note-ink-soft) pl-[0.6em] text-(--note-ink-soft)">
          <Blocks blocks={block.c} {...context} />
        </blockquote>
      )
    case "rule":
      return <hr className="border-(--note-edge)" />
    case "table":
      return (
        <table className="w-full border-collapse text-[0.9em]">
          <tbody>
            {[block.head, ...block.rows].map((row, r) => (
              <tr key={r} className={cn(r === 0 && "font-semibold")}>
                {row.map((cell, c) => (
                  <td
                    key={c}
                    style={{ textAlign: block.align[c] }}
                    className="border-b border-(--note-edge) px-[0.3em] py-[0.15em] align-top"
                  >
                    <Runs runs={cell} />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      )
    case "list": {
      const List = block.ordered ? "ol" : "ul"
      const tasks = block.items.some((item) => item.task)
      return (
        <List
          start={block.ordered && block.start !== 1 ? block.start : undefined}
          className={cn(
            "space-y-[0.2em]",
            tasks
              ? "list-none"
              : block.ordered
                ? "list-decimal pl-[1.3em]"
                : "list-disc pl-[1.2em]",
            "marker:text-(--note-ink-soft)",
          )}
        >
          {block.items.map((item, index) => {
            const task = context.tasks.get(item) ?? -1
            return (
              <li key={index} className={cn(item.task && "flex items-start gap-[0.45em]")}>
                {item.task && (
                  <TaskBox
                    done={Boolean(item.done)}
                    onToggle={context.onToggle ? () => context.onToggle?.(task) : undefined}
                  />
                )}
                <div
                  className={cn(
                    "min-w-0 space-y-[0.35em]",
                    item.done && "text-(--note-ink-soft) line-through decoration-(--note-ink-soft)",
                  )}
                >
                  <Blocks blocks={item.c} {...context} />
                </div>
              </li>
            )
          })}
        </List>
      )
    }
  }
}

function TaskBox({ done, onToggle }: { done: boolean; onToggle?: () => void }) {
  const box = cn(
    "mt-[0.2em] flex size-[1em] shrink-0 items-center justify-center rounded-sm border-[1.5px]",
    done ? "border-(--note-ink) bg-(--note-ink) text-(--note-ground)" : "border-(--note-ink-soft)",
  )
  const mark = done && <Check className="size-[0.75em]" />
  if (!onToggle) {
    return (
      <span role="img" aria-label={done ? "done" : "not done"} className={box}>
        {mark}
      </span>
    )
  }
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={done}
      aria-label={done ? "Mark not done" : "Mark done"}
      // `nodrag` keeps the canvas from reading the press as the start of a move.
      className={cn(box, "nodrag cursor-pointer focus-ring hover:border-(--note-ink)")}
      onPointerDown={(event) => event.stopPropagation()}
      onDoubleClick={(event) => event.stopPropagation()}
      onClick={(event) => {
        event.stopPropagation()
        onToggle()
      }}
    >
      {mark}
    </button>
  )
}

function Runs({ runs }: { runs: Inline[] }) {
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
            className="rounded-sm bg-(--note-wash) px-[0.25em] font-mono text-[0.88em]"
          >
            {run.v}
          </code>
        )
      case "strong":
        return (
          <strong key={index} className="font-bold">
            <Runs runs={run.c} />
          </strong>
        )
      case "em":
        return (
          <em key={index}>
            <Runs runs={run.c} />
          </em>
        )
      case "del":
        return (
          <del key={index} className="text-(--note-ink-soft)">
            <Runs runs={run.c} />
          </del>
        )
      case "link":
        return (
          <a
            key={index}
            href={run.href}
            target="_blank"
            rel="noreferrer noopener"
            className="nodrag underline decoration-(--note-ink-soft) underline-offset-2 hover:decoration-(--note-ink)"
            onPointerDown={(event) => event.stopPropagation()}
            onDoubleClick={(event) => event.stopPropagation()}
          >
            <Runs runs={run.c} />
          </a>
        )
      case "ref":
        return <Fragment key={index}>#{run.n}</Fragment>
      case "mention":
        return <Fragment key={index}>@{run.v}</Fragment>
    }
  })
}
