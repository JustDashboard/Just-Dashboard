"use client"

import { Fragment, useCallback, useMemo, useState } from "react"
import {
  ArrowCircleUp,
  Copy,
  Logs,
  MoreHorizontal,
  Pause,
  Play,
  RotateClockwise,
  StopCircle,
  Terminal,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"
import type { Container } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { ConfirmFn } from "@/components/docker/shared"
import { DimActions, IconAction, RowActions } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

/**
 * One container's verbs, decided once.
 *
 * The table row and the phone card offer the same things to the same people,
 * and before this they each decided that for themselves: the row had five
 * icon-only buttons and no menu, the detail panel had three named buttons and
 * no way to stop anything, and neither of them agreed with the other about
 * which of those a `service.control` operator was allowed to press. Three
 * surfaces, three answers to "what can I do to this container".
 *
 * So the verbs live here as data, and a surface decides only how many of them
 * it has room to draw. What it never decides is what they are called, which
 * capability they need, or what the confirmation says.
 *
 * The labels are the other half of the point. `RotateClockwise` and
 * `ArrowCircleUp` are perfectly good glyphs and neither of them means anything
 * to somebody meeting Docker this week — every verb therefore carries a word,
 * and in the overflow menu a line of plain English under it. A button that
 * needs a tooltip before you dare press it is a button you do not press.
 */

export type ContainerVerb = {
  key: string
  /** The word on the button and in the menu. */
  label: string
  /** One line of plain English, shown in the menu under the label. */
  detail: string
  icon: React.ComponentType<{ className?: string }>
  run: () => void
  /** Drawn inline in a row or a card; the rest go behind the overflow menu. */
  inline?: boolean
  /**
   * The present participle this verb reports while the request is in flight —
   * "Stopping", "Restarting". It is what the row's status cell shows in place
   * of the state Docker is still reporting, and what tells a button in the
   * detail panel that it is the one that was pressed.
   */
  progressive?: string
  danger?: boolean
}

/** Which container is mid-action, and what it is doing — see `useContainerControl`. */
export type PendingMap = Record<string, string>

/**
 * The lifecycle calls, with the row's own "this is happening" state attached.
 *
 * A stop takes a container ten seconds to honour, and the table is fed by a
 * socket that reports the *old* state for every one of them. Without this the
 * row answers a press by doing nothing at all for ten seconds and then jumping,
 * which is indistinguishable from a button that did not work — and is why the
 * second press, on a restart, is so common.
 */
export function useContainerControl(onChanged?: () => void) {
  const [pending, setPending] = useState<PendingMap>({})

  const act = useCallback(
    async (container: Container, action: string, progressive: string, confirmText?: string) => {
      setPending((p) => ({ ...p, [container.id]: progressive }))
      try {
        await post(`/docker/containers/${container.id}/${action}`, undefined, {
          confirm: confirmText,
        })
        notify.success(`${container.name} ${action}ed`)
        onChanged?.()
      } catch (err) {
        notify.error(`Could not ${action} ${container.name}`, err)
        throw err
      } finally {
        setPending((p) => {
          const next = { ...p }
          delete next[container.id]
          return next
        })
      }
    },
    [onChanged],
  )

  return { pending, act }
}

/**
 * Every verb this operator may use on this container, in the order they are
 * wanted: the one thing that changes whether it is serving, then the two ways
 * of looking inside it, then the ones that replace or destroy it.
 */
export function useContainerVerbs({
  container,
  confirm,
  act,
  onOpenTab,
  onChanged,
}: {
  container: Container
  confirm: ConfirmFn
  act: (
    container: Container,
    action: string,
    progressive: string,
    confirm?: string,
  ) => Promise<void>
  /** Opens the detail panel at a named tab — where logs and the shell live. */
  onOpenTab: (tab: string) => void
  onChanged?: () => void
}): ContainerVerb[] {
  const { can } = useAuth()

  return useMemo(() => {
    const verbs: ContainerVerb[] = []
    const running = container.state === "running"
    const paused = container.state === "paused"

    if (can("service.control")) {
      if (paused) {
        verbs.push({
          key: "unpause",
          progressive: "Resuming",
          label: "Resume",
          detail: "Unfreezes it and lets it carry on where it left off.",
          icon: Play,
          inline: true,
          run: () => void act(container, "unpause", "Resuming").catch(() => undefined),
        })
      } else if (!running) {
        verbs.push({
          key: "start",
          progressive: "Starting",
          label: "Start",
          detail: "Runs it again, with everything it was configured with.",
          icon: Play,
          inline: true,
          run: () => void act(container, "start", "Starting").catch(() => undefined),
        })
      }
    }

    if (running && can("destructive")) {
      verbs.push({
        key: "restart",
        progressive: "Restarting",
        label: "Restart",
        detail: "Stops it and starts it again. Whatever it serves is interrupted.",
        icon: RotateClockwise,
        inline: true,
        run: () =>
          confirm({
            title: "Restart container",
            confirmLabel: "Restart",
            description: (
              <p>
                <b>{container.name}</b> will be stopped and started again. Anything it is serving is
                interrupted for as long as it takes to come back.
              </p>
            ),
            action: (phrase) => act(container, "restart", "Restarting", phrase),
          }),
      })
      verbs.push({
        key: "stop",
        progressive: "Stopping",
        label: "Stop",
        detail: "Shuts it down cleanly. It stays here and can be started again.",
        icon: StopCircle,
        inline: true,
        run: () =>
          confirm({
            title: "Stop container",
            confirmLabel: "Stop",
            description: (
              <p>
                <b>{container.name}</b> stops serving immediately. Nothing is deleted — it stays in
                the list and can be started again whenever you want.
              </p>
            ),
            action: (phrase) => act(container, "stop", "Stopping", phrase),
          }),
      })
    }

    verbs.push({
      key: "logs",
      label: "Logs",
      detail: "What the application inside has been printing. The first place to look.",
      icon: Logs,
      run: () => onOpenTab("logs"),
    })

    if (running && can("terminal")) {
      verbs.push({
        key: "shell",
        label: "Open a shell",
        detail: "A command line inside this container — not on the server itself.",
        icon: Terminal,
        run: () => onOpenTab("shell"),
      })
    }

    if (running && can("service.control") && !paused) {
      verbs.push({
        key: "pause",
        progressive: "Pausing",
        label: "Pause",
        detail: "Freezes every process in place without shutting anything down.",
        icon: Pause,
        run: () => void act(container, "pause", "Pausing").catch(() => undefined),
      })
    }

    verbs.push({
      key: "copy-id",
      label: "Copy container id",
      detail: "The handle every docker command on the server wants.",
      icon: Copy,
      run: () => void copyText(container.id, "Container id copied"),
    })

    // Compose owns a stack's containers, so replacing one behind compose's back
    // is undone by the next deploy — silently, and days later. The detail panel
    // says so at length; here the verb is simply not offered.
    if (can("destructive") && !container.composeStack) {
      verbs.push({
        key: "update",
        label: "Update to a newer image",
        detail: `Pulls a newer ${container.image} and rebuilds it with the same settings.`,
        icon: ArrowCircleUp,
        run: () =>
          confirm({
            title: "Update container",
            confirmLabel: "Update",
            description: (
              <>
                <p>
                  Pulls a newer <b>{container.image}</b> and replaces <b>{container.name}</b> with a
                  container built from it, keeping every setting it has now.
                </p>
                <p>
                  Its volumes come with it. Anything written inside the container rather than into a
                  volume does not.
                </p>
              </>
            ),
            action: async (phrase) => {
              await post(
                `/docker/containers/${container.id}/recreate`,
                { pullLatest: true },
                { confirm: phrase },
              )
              onChanged?.()
            },
          }),
      })
    }

    if (can("destructive")) {
      verbs.push({
        key: "remove",
        label: "Remove",
        detail: "Deletes the container. Named volumes and their data are kept.",
        icon: Trash,
        danger: true,
        run: () =>
          confirm({
            title: "Remove container",
            confirmLabel: "Remove",
            description: (
              <p>
                <b>{container.name}</b> will be deleted. This cannot be undone; its anonymous
                volumes are kept.
              </p>
            ),
            action: async (phrase) => {
              await del(`/docker/containers/${container.id}`, {
                confirm: phrase,
                query: { force: true },
              })
              onChanged?.()
            },
          }),
      })
    }

    return verbs
  }, [container, can, confirm, act, onOpenTab, onChanged])
}

/**
 * A row's controls: the two or three that matter as icons, everything else
 * behind one menu.
 *
 * Five icon-only buttons in a table cell is a row that ends in a puzzle. Worse
 * on a phone, where the reveal rule makes all five permanently visible and they
 * take a third of the width. The menu is not a place to hide things — it is
 * where a verb gets a sentence next to it, which is the only form most of these
 * are usable in.
 */
export function ContainerRowActions({
  verbs,
  reveal = true,
  dim,
  className,
}: {
  verbs: ContainerVerb[]
  /** A row that shares its last column with something else hides these until the pointer arrives. */
  reveal?: boolean
  /**
   * For a row whose controls have a column of their own: always drawn, quiet
   * until the pointer is on the row. A reserved column left empty is worse
   * than a busy one — see `DimActions`.
   */
  dim?: boolean
  className?: string
}) {
  const inline = verbs.filter((v) => v.inline)
  const rest = verbs.filter((v) => !v.inline)
  const Wrapper = reveal ? RowActions : dim ? DimActions : PlainActions

  return (
    <Wrapper className={className}>
      {inline.map((verb) => (
        <IconAction
          key={verb.key}
          label={verb.label}
          className={cn(verb.danger && "text-destructive")}
          onClick={(event) => {
            event.stopPropagation()
            verb.run()
          }}
        >
          <verb.icon />
        </IconAction>
      ))}
      {rest.length > 0 && <ContainerMenu verbs={rest} />}
    </Wrapper>
  )
}

function PlainActions({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("flex shrink-0 items-center gap-0.5", className)} {...props} />
}

/**
 * A menu entry's text: the verb, and one line of plain English under it.
 *
 * The stack list's overflow menu had retyped this beside the container menu's
 * copy, and the two had already drifted a pixel apart in line height. One
 * shape, so every Docker menu reads as the same menu.
 */
export function MenuItemBody({ label, detail }: { label: string; detail: string }) {
  return (
    <span className="min-w-0 flex-1">
      <span className="block text-body leading-tight font-medium">{label}</span>
      <span className="mt-0.5 block text-hint leading-snug text-muted-foreground">{detail}</span>
    </span>
  )
}

/** The overflow menu, where a verb is a word and a line rather than a glyph. */
export function ContainerMenu({
  verbs,
  align,
  disabled,
}: {
  verbs: ContainerVerb[]
  align?: "start" | "end"
  /** A command is already in flight; every verb here would collide with it. */
  disabled?: boolean
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="More actions"
          onClick={(event) => event.stopPropagation()}
          className="[&_svg:not([class*='size-'])]:size-3.5"
        >
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align={align ?? "end"} className="w-68">
        {verbs.map((verb, i) => (
          <Fragment key={verb.key}>
            {verb.danger && i > 0 && <DropdownMenuSeparator />}
            <DropdownMenuItem
              variant={verb.danger ? "destructive" : "default"}
              disabled={disabled}
              className="items-start gap-2.5 py-1.5"
              onSelect={(event) => {
                event.preventDefault()
                verb.run()
              }}
            >
              <verb.icon className="mt-0.5 size-3.5 shrink-0" />
              <MenuItemBody label={verb.label} detail={verb.detail} />
            </DropdownMenuItem>
          </Fragment>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
