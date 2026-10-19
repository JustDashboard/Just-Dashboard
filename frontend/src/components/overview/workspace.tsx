"use client"

import { useMemo } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight } from "@/components/icons"
import type { BoardSummary } from "@/lib/boards"
import { plural, relativeTime } from "@/lib/format"
import type { StickySummary } from "@/lib/stickyboard"
import type { TerminalList } from "@/lib/types"
import type { PollState } from "@/hooks/use-poll"
import { sessionLabel, sessionProgram } from "@/lib/terminal-activity"
import { ExcalidrawGlyph } from "@/components/boards/glyphs"
import { DrawingPreview } from "@/components/boards/drawing-preview"
import { CONTROL, ChoiceList, ChoiceRow } from "@/components/flow"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, programProduct } from "@/components/product-logo"
import { EmptyState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { BoardPreview } from "@/components/stickyboard/board-preview"
import { StickyGlyph } from "@/components/stickyboard/glyphs"
import { Button } from "@/components/ui/button"
import { BlurFade } from "@/components/ui/blur-fade"
import { Skeleton } from "@/components/ui/skeleton"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
import { recentWork, shellOrder, type RecentWork } from "./order"
import { SectionLink } from "./section-link"

/** Enough to go back to what was open this morning without becoming the terminal's rail. */
const SHOWN_SHELLS = 4
/** Two rows of two: the last few things drawn, not a second copy of either list. */
const SHOWN_WORK = 4

/** Where a new shell opens: the signed-in account's home, as the terminal's own New does. */
export function newShellHref(list: TerminalList | undefined) {
  return list?.login.home ? `/terminal?cwd=${encodeURIComponent(list.login.home)}` : "/terminal"
}

/**
 * The shells open on the server, the one working first, each drawn as the
 * program in it — Claude, Neovim, psql — so the one somebody left an agent
 * running in is found before a name is read. A row opens the terminal on that
 * session rather than on whichever was last on screen.
 */
export function ShellsPanel({ read }: { read: PollState<TerminalList> }) {
  const { data, error, loading } = read
  const sessions = useMemo(() => shellOrder(data?.sessions ?? []), [data])
  const working = sessions.filter((session) => session.live && session.working).length
  const home = data?.login.home

  return (
    <Panel plain>
      <PanelHeader
        title="Terminal"
        actions={
          <div className="flex h-8 items-center gap-3 text-hint">
            {sessions.length > 0 && (
              <span className="numeric text-muted-foreground">
                {plural(sessions.length, "session")}
                {working > 0 && ` · ${working} working`}
              </span>
            )}
            <SectionLink href="/terminal">Terminal</SectionLink>
          </div>
        }
      />
      <PanelBody flush>
        {loading && !data ? (
          <LoadingRows rows={3} className="[&>*]:h-14 [&>*]:rounded-xl" />
        ) : !data ? (
          <p className="py-2 text-body text-muted-foreground">
            {error ? "The sessions could not be read." : "Nothing to show."}
          </p>
        ) : !data.enabled ? (
          <p className="py-2 text-body text-muted-foreground">
            The terminal is switched off on this server.
          </p>
        ) : sessions.length === 0 ? (
          <EmptyState
            mark={<ProductLogo id="terminal" />}
            title="No shells open"
            description={`A new one signs in as ${data.login.user}, in ${data.login.home}.`}
            action={
              <Button size="sm" variant="outline" asChild>
                <Link href={newShellHref(data)}>New shell</Link>
              </Button>
            }
            className="py-8"
          />
        ) : (
          <ChoiceList aria-label="Terminal sessions" className="animate-rise">
            {sessions.slice(0, SHOWN_SHELLS).map((session) => {
              const label = sessionLabel(session, {})
              const program = sessionProgram(session, {})
              return (
                <ChoiceRow
                  key={session.id}
                  href={`/terminal?session=${encodeURIComponent(session.id)}`}
                  verb={`Open ${label}`}
                  leading={<ProductLogo id={programProduct(program) ?? "terminal"} size="sm" />}
                  title={label}
                  description={
                    <>
                      {session.cwd && (
                        <span className="font-mono">{tilde(session.cwd, home)} · </span>
                      )}
                      {plural(session.windows, "window")}
                      {" · started "}
                      {relativeTime(session.createdAt)}
                    </>
                  }
                  trailing={<ShellState session={session} />}
                />
              )
            })}
          </ChoiceList>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * The terminal rail's reading in words: green while something in it works,
 * amber while it sits — at a prompt, or in a program waiting for input — and
 * grey once its process has gone.
 */
function ShellState({ session }: { session: TerminalList["sessions"][number] }) {
  if (!session.live) return <Status tone="stopped" label="Ended" className="text-hint" />
  if (session.working) return <Status tone="running" label="Working" className="text-hint" />
  return <Status tone="warning" label="Idle" className="text-hint" />
}

/** A path under the account's home, the way its prompt writes it. */
function tilde(path: string, home: string | undefined) {
  if (!home || home === "/") return path
  if (path === home) return "~"
  return path.startsWith(`${home}/`) ? `~${path.slice(home.length)}` : path
}

/**
 * The drawings and sticky boards edited last, as one list, each drawn as
 * itself: the picture is the thing, so a board of pink questions and the
 * network sketch are told apart before their names are read.
 */
export function RecentWorkPanel({
  drawings,
  boards,
}: {
  /** Undefined where the section is kept out of the sidebar, which is not the same as none. */
  drawings?: PollState<BoardSummary[]>
  boards?: PollState<StickySummary[]>
}) {
  const work = useMemo(
    () => recentWork(drawings?.data, boards?.data, SHOWN_WORK),
    [drawings?.data, boards?.data],
  )
  const waiting =
    (drawings?.loading && !drawings.data) || (boards?.loading && !boards.data) || false
  const failed =
    (drawings && !drawings.data && drawings.error) || (boards && !boards.data && boards.error)

  return (
    <Panel plain>
      <PanelHeader
        title="Drawings & boards"
        actions={
          <div className="flex h-8 items-center gap-3 text-hint">
            {drawings && <SectionLink href="/excalidraw">Excalidraw</SectionLink>}
            {boards && <SectionLink href="/stickyboard">Stickyboard</SectionLink>}
          </div>
        }
      />
      <PanelBody flush>
        {waiting && work.length === 0 ? (
          <ul aria-hidden className="grid grid-cols-2 gap-3">
            {Array.from({ length: SHOWN_WORK }).map((_, index) => (
              <li key={index}>
                <Skeleton className="h-40 rounded-xl" />
              </li>
            ))}
          </ul>
        ) : work.length === 0 ? (
          failed ? (
            <p className="py-2 text-body text-muted-foreground">
              The drawings and boards could not be read.
            </p>
          ) : (
            <EmptyState
              mark={<ProductLogo id="excalidraw" />}
              title="Nothing drawn yet"
              description="A drawing or a board you edit is here the next time you open the Overview."
              className="py-8"
            />
          )
        ) : (
          <ul aria-label="Recent drawings and boards" className="grid grid-cols-2 gap-3">
            {work.map((item, index) => (
              <WorkCard key={item.key} item={item} index={index} />
            ))}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}

/** One drawing or board: its picture over its name, the tool it lives in and when it changed. */
function WorkCard({ item, index }: { item: RecentWork; index: number }) {
  const router = useRouter()
  const Glyph = item.kind === "drawing" ? ExcalidrawGlyph : StickyGlyph
  return (
    <li className="min-w-0">
      <BlurFade delay={index * 0.04} className="h-full">
        <SpotlightBorder className="h-full">
          <div
            onClick={(event) => {
              const target = event.target as HTMLElement
              if (!event.currentTarget.contains(target) || target.closest(CONTROL)) return
              router.push(item.href)
            }}
            className="group/choice flex h-full min-w-0 cursor-pointer flex-col rounded-xl"
          >
            {item.kind === "drawing" ? (
              <DrawingPreview
                preview={item.drawing.preview}
                className="h-24 w-full overflow-hidden rounded-t-xl border-b border-hairline sm:h-28"
              />
            ) : (
              <BoardPreview
                preview={item.board.preview}
                className="h-24 w-full rounded-t-xl border-b border-hairline bg-background sm:h-28"
              />
            )}
            <div className="flex min-w-0 items-start gap-2 px-3 py-2.5">
              <div className="min-w-0 flex-1">
                <Link
                  href={item.href}
                  aria-label={`Open ${item.name}`}
                  className="block max-w-full truncate rounded-sm text-body font-medium focus-ring"
                >
                  {item.name}
                </Link>
                <p className="mt-0.5 flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
                  <Glyph aria-hidden className="size-3 shrink-0" />
                  <span className="truncate">edited {relativeTime(item.updatedAt)}</span>
                </p>
              </div>
              <ArrowRight
                aria-hidden
                className="mt-1 size-3.5 shrink-0 text-muted-foreground transition-colors group-hover/choice:text-foreground"
              />
            </div>
          </div>
        </SpotlightBorder>
      </BlurFade>
    </li>
  )
}
