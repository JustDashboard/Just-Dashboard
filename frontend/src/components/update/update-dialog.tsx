"use client"

import { useState } from "react"
import {
  ArrowCircleUp,
  Layers,
  RefreshClockwise,
  ShieldCheck,
  Stopwatch,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { useSelfUpdate } from "@/hooks/use-self-update"
import { LogoGlyph } from "@/components/logo"
import { ProductGlyph } from "@/components/product-logo"
import { KindBand, UpdateWire } from "@/components/update/update-art"
import { releaseDay } from "@/components/update/release-notes"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"

/**
 * "Update to 0.7.5?", asked as what it is.
 *
 * This was the shared `ConfirmDialog`, and that dialog exists for acts that
 * are awkward to undo — most of them deletions — so its command is always
 * destructive red. An update is the opposite kind of act: the operator is
 * taking something, and the dialog asking them to confirm it wore the same
 * red as "Delete database". So it has its own. The command is the brand face
 * (§3: the one action on the surface), and the dialog says the three things
 * somebody about to replace their dashboard wants to know:
 *
 * - **what arrives** — the installed version wired to the newest one, the
 *   releases between them, and what they hold by kind;
 * - **what has to be done by hand**, if anything — a breaking release's note
 *   and uncommitted edits in the checkout, never folded away;
 * - **what happens** — pulled from where, rebuilt, restarted — and what does
 *   not: the data, the accounts and the settings.
 *
 * It is a `Modal` like every other dialog — the title strip, a body that
 * scrolls, the command in the footer — and its body opens on a picture rather
 * than a sentence: the two versions wired together on arrival's light
 * (`.update-art`, design system §16), for the reason the sidebar notice stands
 * on it: a new version is the one thing in the shell that announces something
 * arriving.
 *
 * No phrase to type: the route is destructive, but installing an update is
 * not one of the unrecoverable acts that require one (security invariant 3).
 */
export function UpdateDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { report, install } = useSelfUpdate()
  const [busy, setBusy] = useState(false)
  if (!report?.latest) return null

  const target = report.latest
  const releases = report.releases
  const dirty = report.install.dirty ?? []
  const breaking = releases.filter((release) => release.breaking && release.breakingNote)

  const start = async () => {
    setBusy(true)
    try {
      await install("")
      onOpenChange(false)
    } catch (err) {
      notify.error(`Could not start the update to ${target}`, err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      size="sm"
      title={`Update to ${target}`}
      description={`Installs ${target} over ${report.version}: pulls ${report.check.repo} at ${report.check.ref}, rebuilds every image in this stack and restarts it, the dashboard included.`}
      bodyClassName="p-0"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Not now
          </Button>
          <Button onClick={start} pending={busy}>
            <ArrowCircleUp />
            Update to {target}
          </Button>
        </>
      }
    >
      <div
        aria-hidden
        className="update-art grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 border-b border-hairline px-5 pt-6 pb-5"
      >
        <VersionMark />
        <UpdateWire />
        <VersionMark next />
        <VersionLabel label="Installed" version={report.version} />
        <span />
        <VersionLabel label="Latest" version={target} next />
      </div>

      <div className="space-y-5 p-5">
        {releases.length > 0 && (
          <section className="space-y-3">
            <h3 className="eyebrow">
              {releases.length === 1 ? "What's in it" : `${releases.length} releases ahead`}
            </h3>
            <ul className="space-y-2">
              {releases.slice(0, 4).map((release) => (
                <li key={release.version} className="flex min-w-0 items-baseline gap-3">
                  <span className="numeric w-11 shrink-0 text-xs font-semibold">
                    {release.version}
                  </span>
                  <span className="min-w-0 flex-1 truncate text-body">{release.title}</span>
                  <time
                    dateTime={release.date}
                    className="shrink-0 text-hint text-muted-foreground"
                  >
                    {releaseDay(release.date)}
                  </time>
                </li>
              ))}
            </ul>
            {releases.length > 4 && (
              <p className="text-hint text-muted-foreground">
                and {releases.length - 4} more, back to {releases[releases.length - 1].version}
              </p>
            )}
            <KindBand releases={releases} />
          </section>
        )}

        {/* A breaking release is never folded away: the flag means the
              operator has something to do by hand, and a warning met after
              pressing Update is a warning met too late. */}
        {breaking.map((release) => (
          <Notice
            key={release.version}
            tone="warning"
            icon={Warning}
            title={
              breaking.length === 1 ? (
                "Before you update"
              ) : (
                <>
                  Before you update — <span className="numeric">{release.version}</span>
                </>
              )
            }
          >
            {release.breakingNote}
          </Notice>
        ))}

        {dirty.length > 0 && (
          <Notice title="The install directory has uncommitted changes" icon={Warning}>
            <p>
              The update fast-forwards rather than resetting, so they survive unless it changes the
              same lines — and then it stops and says so rather than discarding them.
            </p>
            <ul className="mt-1 space-y-0.5 font-mono text-hint">
              {dirty.slice(0, 4).map((line) => (
                <li key={line}>{line}</li>
              ))}
            </ul>
          </Notice>
        )}

        <section className="space-y-3">
          <h3 className="eyebrow">What happens</h3>
          <ol className="space-y-2.5 text-body">
            <li className="flex min-w-0 items-center gap-2.5">
              <ProductGlyph id="github" className="size-4" />
              <span className="min-w-0 flex-1 truncate">
                Pulls <span className="font-mono text-xs">{report.check.repo}</span> at{" "}
                <span className="font-mono text-xs">{report.check.ref}</span>
              </span>
            </li>
            <li className="flex min-w-0 items-center gap-2.5">
              <Layers aria-hidden className="size-4 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1">Rebuilds every image in this stack</span>
            </li>
            <li className="flex min-w-0 items-center gap-2.5">
              <RefreshClockwise aria-hidden className="size-4 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1">Restarts it, the dashboard included</span>
            </li>
          </ol>
        </section>

        <ul className="space-y-2 border-t border-hairline pt-4 text-xs leading-relaxed">
          <li className="flex items-start gap-2.5">
            <ShieldCheck aria-hidden className="mt-0.5 size-3.5 shrink-0 text-success" />
            <span>Your data, accounts and settings are untouched.</span>
          </li>
          <li className="flex items-start gap-2.5 text-muted-foreground">
            <Stopwatch aria-hidden className="mt-0.5 size-3.5 shrink-0" />
            <span>
              A few minutes, in a container of its own so it survives the restart. Keep this tab
              open and it picks back up by itself.
            </span>
          </li>
        </ul>
      </div>
    </Modal>
  )
}

/**
 * The dashboard's mark at either end of the wire: the installed version a
 * tile of plain ground with the J in grey, the newest one ringed and lit in
 * the brand's tint the way the rack is drawn for the server the reader is on.
 */
function VersionMark({ next = false }: { next?: boolean }) {
  return next ? (
    <span aria-hidden className="flex shrink-0 rounded-[0.875rem] border border-rule-brand p-[2px]">
      <span className="flex size-[2.375rem] items-center justify-center rounded-[0.625rem] bg-wash-brand text-brand">
        <LogoGlyph className="h-5 w-auto" />
      </span>
    </span>
  ) : (
    <span
      aria-hidden
      className="flex size-11 shrink-0 items-center justify-center rounded-xl border border-hairline bg-background text-muted-foreground"
    >
      <LogoGlyph className="h-5 w-auto" />
    </span>
  )
}

function VersionLabel({
  label,
  version,
  next = false,
}: {
  label: string
  version: string
  next?: boolean
}) {
  return (
    <span className={next ? "grid justify-items-end" : "grid justify-items-start"}>
      <span className="eyebrow">{label}</span>
      <span
        className={
          next
            ? "numeric text-base leading-tight font-semibold"
            : "numeric text-sm leading-tight text-muted-foreground"
        }
      >
        {version}
      </span>
    </span>
  )
}

/** Drives one UpdateDialog: the sidebar notice and the Version page each hold one. */
export function useUpdateDialog() {
  const [open, setOpen] = useState(false)
  const dialog = <UpdateDialog open={open} onOpenChange={setOpen} />
  return { openUpdate: () => setOpen(true), dialog }
}
