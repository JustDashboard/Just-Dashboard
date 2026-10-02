"use client"

import { useState } from "react"
import { SidebarRightOpen, Trash } from "@/components/icons"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { usePanelSize } from "@/lib/panel-size"
import { forgetMemoryState, useViewState } from "@/lib/view-state"
import { useConfirm } from "@/components/confirm-dialog"
import { IconAction } from "@/components/icon-action"
import { PaneHeader } from "@/components/panel"
import { ResizeHandle } from "@/components/resize-handle"
import { EmptyState, Notice } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import { Skeleton } from "@/components/ui/skeleton"
import { EngineMark, SectionFrame } from "@/components/database/kit"
import { redisCommands } from "@/components/database/redis/api"
import { accept } from "@/components/database/redis/console/complete"
import { ConsoleView } from "@/components/database/redis/console/console-view"
import { CommandHelper } from "@/components/database/redis/console/helper"
import { MonitorView } from "@/components/database/redis/console/monitor"
import { PubSubView } from "@/components/database/redis/console/pubsub"
import { DbPicker } from "@/components/database/redis/db-picker"
import { useFocusReturn } from "@/components/database/redis/use-focus-return"
import { useRedis } from "@/components/database/redis/use-redis"

const HELPER = { base: 320, min: 260, max: 520 }

type View = "console" | "pubsub" | "monitor"

/**
 * The console: commands typed at the server, with the server's own reference
 * beside them — and, where the server offers them, its two live feeds.
 *
 * One frame. The strip across the top says which of the three is on screen
 * and which numbered database a command runs in; the transcript and its
 * prompt fill the rest, with the command reference as a column that can be
 * put away. Published messages and the monitor are views of the same page,
 * since they are the two things a console line cannot do: a subscription and
 * a monitor each take a connection over, so the console refuses them and
 * points here.
 */
export function RedisConsole() {
  const redis = useRedis()
  const { id, db, server, engine, param, select, canRun, admin } = redis
  const { confirm, dialog } = useConfirm()
  useFocusReturn()
  const features = server.data?.features

  const views: { id: View; label: string }[] = [
    { id: "console", label: "Console" },
    ...(engine.can("pubsub") ? [{ id: "pubsub" as const, label: "Pub/Sub" }] : []),
    ...(admin && engine.can("monitor") && features?.monitor !== false
      ? [{ id: "monitor" as const, label: "Monitor" }]
      : []),
  ]
  const asked = param("view")
  const view: View = views.some((entry) => entry.id === asked) ? (asked as View) : "console"

  const reference = usePoll((signal) => redisCommands(id, signal), 0, [id])
  const [line, setLine] = useState("")

  const wide = useMediaQuery("(min-width: 1024px)")
  const [helperShown, setHelperShown] = useViewState(`databases.${id}.redis.helper`, true)
  const [helperOver, setHelperOver] = useState(false)
  const helperVisible = view === "console" && (wide ? helperShown : helperOver)
  const [helperWidth, setHelperWidth, resetHelperWidth] = usePanelSize(
    "databases.redis.helper",
    HELPER.base,
  )
  const helperPx = Math.min(Math.max(helperWidth, HELPER.min), HELPER.max)

  return (
    <SectionFrame section="query">
      {dialog}
      {server.data?.notice && (
        <Notice title="This is not a single standalone server" className="shrink-0">
          {server.data.notice}
        </Notice>
      )}
      <div
        style={{ "--jd-redis-helper": `${helperPx}px` } as React.CSSProperties}
        className="relative flex min-h-0 min-w-0 flex-1 overflow-hidden rounded-xl border bg-card"
      >
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <PaneHeader className="gap-1 py-0 pl-0">
            {/* Pressed buttons, not a landmark: three readings of one page. */}
            <div role="group" aria-label="Console views" className="flex min-w-0 self-stretch">
              {views.map((entry) => (
                <button
                  key={entry.id}
                  type="button"
                  aria-pressed={view === entry.id}
                  onClick={() => select({ view: entry.id === "console" ? null : entry.id })}
                  className={tabClasses(view === entry.id, "h-10")}
                >
                  {entry.label}
                </button>
              ))}
            </div>
            <span className="min-w-0 flex-1" />
            {view === "console" && (
              <>
                {server.data ? (
                  <DbPicker server={server.data} db={db} onChange={redis.setDb} />
                ) : (
                  !server.error && <Skeleton className="h-7 w-16" />
                )}
                <IconAction
                  label="Clear the transcript"
                  className="size-7"
                  onClick={() => forgetMemoryState(`databases.${id}.redis.console.log`)}
                >
                  <Trash />
                </IconAction>
                {!helperVisible && (
                  <IconAction
                    label="Show the commands"
                    aria-pressed={false}
                    className="size-7"
                    onClick={() => (wide ? setHelperShown(true) : setHelperOver(true))}
                  >
                    <SidebarRightOpen />
                  </IconAction>
                )}
              </>
            )}
          </PaneHeader>
          {view === "pubsub" ? (
            <PubSubView redis={redis} />
          ) : view === "monitor" ? (
            <MonitorView redis={redis} />
          ) : canRun ? (
            <ConsoleView
              redis={redis}
              commands={reference.data?.commands ?? NO_COMMANDS}
              line={line}
              onLine={setLine}
              confirm={confirm}
              views={views.flatMap((entry) => (entry.id === "console" ? [] : [entry.id]))}
              onView={(next) => select({ view: next })}
            />
          ) : (
            <EmptyState
              mark={<EngineMark engine={engine} />}
              className="min-h-0 flex-1 border-0"
              title="Your role cannot run commands"
              description="Running a command, even one that only reads, needs the service control capability. The command reference is still here to read."
            />
          )}
        </div>
        {helperVisible && (
          <div className="relative flex shrink-0 border-hairline max-lg:absolute max-lg:inset-0 max-lg:z-20 lg:w-(--jd-redis-helper) lg:border-l">
            <ResizeHandle
              side="right"
              label="Commands panel width"
              value={helperPx}
              min={HELPER.min}
              max={HELPER.max}
              onChange={(px, commit) => setHelperWidth(px, commit)}
              onReset={resetHelperWidth}
              className="absolute inset-y-0 -left-1 z-20"
            />
            <CommandHelper
              reference={reference.data}
              error={reference.error}
              onRetry={reference.refresh}
              onInsert={(command) => {
                setLine(accept(command))
                if (!wide) setHelperOver(false)
              }}
              onClose={() => (wide ? setHelperShown(false) : setHelperOver(false))}
            />
          </div>
        )}
      </div>
    </SectionFrame>
  )
}

const NO_COMMANDS: never[] = []
