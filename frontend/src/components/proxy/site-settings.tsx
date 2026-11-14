"use client"

import { useEffect, useEffectEvent, useSyncExternalStore } from "react"
import { createPortal } from "react-dom"
import Link from "next/link"
import { cn } from "@/lib/utils"
import { plural } from "@/lib/format"
import { Modal } from "@/components/modal"
import { Status, StatusDot } from "@/components/status-dot"
import { ChipCount } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { PendingStrip } from "@/components/deploy/settings/pending-strip"
import {
  SETTINGS_VIEWS,
  SETTINGS_VIEW_LABEL,
  leavesSettings,
  siteSettingsHref,
  type SettingsView,
} from "@/components/proxy/site-tabs"

/**
 * The chrome of a site's Settings: what the form's sheet was — a header, a
 * footer holding two Saves and a pane beside the fields — drawn as a
 * deployment's settings are. The fields and everything they do stay in
 * `site-form.tsx`; this file is only where they stand.
 *
 *   `SettingsParts` — the parts of the form (`?view=`), each a link, with a
 *   mark on the ones holding an unsaved change or a failed check.
 *   `UnsavedStrip`  — the strip at the head of the column that says what is
 *   unsaved, each part a way back to it.
 *   `SiteSaveBar`   — the one floating bar, which carries the form's own two
 *   saves rather than the single Save a deployment's forms share: a site is
 *   saved and reloaded, or saved for a reload later, and a disabled one is
 *   saved as it is or enabled on purpose.
 *   `useLeaveGuard` and `DiscardDialog` — the question the sheet asked on
 *   closing, asked on leaving.
 */

/** A mark beside a part's name: how many, and how bad. */
export type PartMark = { count: number; tone: "danger" | "warning" | "muted" }

const MARK_TONE: Record<PartMark["tone"], string> = {
  danger: "text-destructive opacity-100",
  warning: "text-warning opacity-100",
  muted: "",
}

/**
 * The parts of a site's Settings. Links rather than buttons, so each part
 * has an address, opens in a new tab, and the browser's Back steps out of
 * Settings rather than through every part visited.
 */
export function SettingsParts({
  name,
  view,
  unsaved,
  marks,
}: {
  name: string
  view: SettingsView
  /** The parts holding an unsaved change: each carries a dot. */
  unsaved: SettingsView[]
  marks: Partial<Record<SettingsView, PartMark>>
}) {
  return (
    <nav
      aria-label="Parts of the settings"
      className={cn(
        "flex min-w-0 [scrollbar-width:none] items-center gap-1.5 overflow-x-auto [&::-webkit-scrollbar]:hidden",
        // Bled to the page's gutter on a phone, so the part cut off at the
        // edge says there are more, as a chip strip does.
        // The room above and below is the focus ring's, which the scroller
        // would clip; only the top is taken back, so the strip under the
        // parts keeps the column's gap.
        "max-sm:-mx-5 max-sm:-mt-1 max-sm:px-5 max-sm:py-1 sm:flex-wrap sm:overflow-visible",
      )}
    >
      {SETTINGS_VIEWS.map((key) => {
        const here = key === view
        const mark = marks[key]
        const dirty = unsaved.includes(key)
        return (
          <Link
            key={key}
            href={siteSettingsHref(name, key)}
            replace
            scroll={false}
            aria-current={here ? "page" : undefined}
            aria-label={
              dirty
                ? `${SETTINGS_VIEW_LABEL[key]}, unsaved changes`
                : mark
                  ? `${SETTINGS_VIEW_LABEL[key]}, ${mark.count}`
                  : undefined
            }
            className={cn(
              "inline-flex h-7 shrink-0 items-center gap-1.5 rounded-md border px-2.5 text-hint font-medium whitespace-nowrap focus-ring transition-colors",
              here
                ? "border-border-strong bg-accent text-foreground"
                : "border-input text-muted-foreground hover:bg-row-hover hover:text-foreground",
            )}
          >
            {SETTINGS_VIEW_LABEL[key]}
            {mark && mark.count > 0 && (
              <ChipCount className={MARK_TONE[mark.tone]}>{mark.count}</ChipCount>
            )}
            {dirty && <StatusDot tone="warning" className="size-1.5" />}
          </Link>
        )
      })}
    </nav>
  )
}

/**
 * What is unsaved, at the head of the column: how many fields, how many
 * lines of the file a save would change, and the parts they are in.
 */
export function UnsavedStrip({
  name,
  view,
  fields,
  lines,
  parts,
}: {
  name: string
  view: SettingsView
  /** How many fields differ from the file. */
  fields: number
  /** How many lines of the file a save adds and removes, once the preview says. */
  lines: number
  parts: SettingsView[]
}) {
  return (
    <PendingStrip
      label="Unsaved changes"
      fold="Where they are"
      headline={
        <Status
          tone="warning"
          label={fields === 1 ? "1 unsaved change" : `${fields} unsaved changes`}
        />
      }
      detail={
        lines > 0 ? (
          <>
            <Link
              href={siteSettingsHref(name, "file")}
              replace
              scroll={false}
              className="rounded-sm focus-ring hover:underline"
            >
              {plural(lines, "line")} of the file
            </Link>{" "}
            would change
          </>
        ) : undefined
      }
      items={parts.map((part) => ({
        name: SETTINGS_VIEW_LABEL[part],
        state: "changed",
        href: siteSettingsHref(name, part),
        current: part === view,
      }))}
    />
  )
}

/** One of the bar's saves: its word, and whether it is the one in flight. */
export type SaveCommand = {
  label: string
  run: () => void
  pending: boolean
}

// The content area beside the rail, which the bar centres on, as a
// deployment's does. It is in the document for the whole life of the shell.
const subscribe = () => () => {}
const contentArea = () => document.querySelector<HTMLElement>('[data-slot="sidebar-inset"]')
const noContentArea = () => null

// Read once the bar is on screen, which is only ever in the browser.
const shortcut = () => (/Mac|iPhone|iPad/.test(navigator.userAgent) ? "⌘S" : "Ctrl S")

/**
 * The page's one Save, floating at the foot of the window while there is
 * something to save. `headline` says what that is — the unsaved changes, or
 * a file the form would rewrite — and `note` what the save does, which was
 * the sheet's footer line. The primary command takes Ctrl or Cmd+S.
 */
export function SiteSaveBar({
  headline,
  note,
  primary,
  secondary,
  onDiscard,
  disabled,
  saving,
}: {
  headline: string
  note: React.ReactNode
  primary: SaveCommand
  secondary?: SaveCommand
  /** Puts the fields back as the file has them; absent where nothing was edited. */
  onDiscard?: () => void
  /** A save could not be sent as the draft stands. */
  disabled: boolean
  saving: boolean
}) {
  const host = useSyncExternalStore(subscribe, contentArea, noContentArea)
  if (!host) return null
  return createPortal(
    <div className="pointer-events-none absolute inset-x-0 bottom-4 z-40 flex justify-center px-3 md:bottom-6">
      <div
        role="region"
        aria-label="Save changes"
        aria-live="polite"
        className="pointer-events-auto flex w-full max-w-2xl animate-rise flex-wrap items-center gap-x-3 gap-y-2 rounded-xl border border-border-strong bg-popover py-2.5 pr-2.5 pl-4 text-popover-foreground shadow-lg"
      >
        <div key={saving ? "saving" : "dirty"} className="min-w-0 flex-[1_1_14rem] animate-rise">
          <p className="flex min-w-0 items-center gap-2 text-body leading-snug font-medium">
            <StatusDot tone="warning" />
            {saving ? (
              <TextShimmer>Saving…</TextShimmer>
            ) : (
              <span className="numeric">{headline}</span>
            )}
          </p>
          <p className="mt-0.5 line-clamp-2 pl-4 text-xs text-muted-foreground">{note}</p>
        </div>
        <div className="ml-auto flex shrink-0 flex-wrap items-center justify-end gap-1.5">
          {onDiscard && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={saving}
              onClick={onDiscard}
              className="max-sm:h-10"
            >
              Discard
            </Button>
          )}
          {secondary && (
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={disabled || saving}
              pending={secondary.pending}
              onClick={secondary.run}
              className="max-sm:h-10"
            >
              {secondary.label}
            </Button>
          )}
          <Button
            type="button"
            size="sm"
            disabled={disabled || saving}
            pending={primary.pending}
            onClick={primary.run}
            aria-keyshortcuts="Control+S Meta+S"
            className="max-sm:h-10"
          >
            {primary.label}
            <kbd
              aria-hidden
              className={cn(
                "ml-0.5 font-sans text-hint font-normal opacity-60 max-sm:hidden",
                primary.pending && "hidden",
              )}
            >
              {shortcut()}
            </kbd>
          </Button>
        </div>
      </div>
    </div>,
    host,
  )
}

/**
 * Catches a press on a link that leads out of a site's Settings while they
 * hold unsaved changes, before the router acts on it, and hands the address
 * over so the reader can be asked first.
 *
 * The App Router cannot be told to wait: once a link's own handler has run
 * the page is gone. A capture listener on the document runs before it. A
 * link inside the form itself is a trip the form sends the reader on — to
 * issue the certificate it names — and is let through: the draft is kept for
 * the tab, as the sheet kept it.
 */
export function useLeaveGuard(active: boolean, onLeave: (href: string) => void) {
  const leave = useEffectEvent(onLeave)
  useEffect(() => {
    if (!active) return
    const intercept = (event: MouseEvent) => {
      if (event.defaultPrevented || event.button !== 0) return
      if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
      const target = event.target instanceof Element ? event.target.closest("a[href]") : null
      if (!(target instanceof HTMLAnchorElement)) return
      if (target.target && target.target !== "_self") return
      if (target.hasAttribute("download")) return
      if (target.closest('[data-slot="site-settings"]')) return
      const here = new URL(window.location.href)
      const url = new URL(target.href, here)
      if (!leavesSettings(here, url)) return
      event.preventDefault()
      event.stopPropagation()
      leave(`${url.pathname}${url.search}${url.hash}`)
    }
    document.addEventListener("click", intercept, true)
    return () => document.removeEventListener("click", intercept, true)
  }, [active])
}

/** Asked before unsaved changes are left behind. Nothing is discarded by closing it. */
export function DiscardDialog({
  open,
  onStay,
  onDiscard,
  children,
}: {
  open: boolean
  onStay: () => void
  onDiscard: () => void
  children: React.ReactNode
}) {
  return (
    <Modal
      open={open}
      onOpenChange={(next) => !next && onStay()}
      size="sm"
      title="Discard changes?"
      footer={
        <>
          <Button variant="outline" onClick={onStay}>
            Keep editing
          </Button>
          <Button variant="destructive" onClick={onDiscard}>
            Discard
          </Button>
        </>
      }
    >
      <p className="text-body leading-relaxed">{children}</p>
    </Modal>
  )
}
