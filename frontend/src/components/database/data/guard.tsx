"use client"

import { useEffect } from "react"
import { FormFact, FormFacts } from "@/components/form"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import type { ChangeCounts } from "@/components/database/grid"
import { EngineMark } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"
import { changeSummary } from "@/components/database/data/rows"

/**
 * Asked before staged edits are left behind: another table, another page of
 * this one, another sort or filter, a refresh, a link out of the editor.
 *
 * Every one of those takes away the rows the edits were made on, so the
 * reader chooses — go back to them, read what they would do, or let them go
 * and carry on to where they were heading. Nothing is discarded by closing
 * the dialog: Escape and the corner are "keep editing".
 */
export function LeaveGuard({
  open,
  table,
  counts,
  heading,
  onStay,
  onReview,
  onDiscard,
}: {
  open: boolean
  table: string
  counts: ChangeCounts
  /** Where the reader was going: "Opening orders", "Turning the page". */
  heading: string
  onStay: () => void
  /** Stay, and open the statements the set would run. Absent when the set cannot be sent. */
  onReview?: () => void
  onDiscard: () => void
}) {
  const { engine } = useDatabase()
  return (
    <Modal
      open={open}
      onOpenChange={(next) => !next && onStay()}
      size="sm"
      title="Unapplied changes"
      description="This table has staged changes that have not been applied"
      footer={
        <>
          <Button variant="outline" onClick={onStay}>
            Keep editing
          </Button>
          {onReview && (
            <Button variant="outline" onClick={onReview}>
              Review them
            </Button>
          )}
          <Button variant="destructive" onClick={onDiscard}>
            Discard and go on
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0 space-y-0.5">
            <p className="truncate font-mono text-body font-medium">{table}</p>
            <FormFacts>
              <FormFact label="Staged">{changeSummary(counts)}</FormFact>
            </FormFacts>
          </div>
        </div>
        <p className="text-body leading-relaxed">
          {heading} leaves the rows these changes were made on. They have not been written to the
          database; going on throws them away.
        </p>
      </div>
    </Modal>
  )
}

/** The browser's own question before a reload or a closed tab loses staged edits. */
export function useUnloadGuard(dirty: boolean) {
  useEffect(() => {
    if (!dirty) return
    const ask = (event: BeforeUnloadEvent) => {
      event.preventDefault()
    }
    window.addEventListener("beforeunload", ask)
    return () => window.removeEventListener("beforeunload", ask)
  }, [dirty])
}

/**
 * Catches a press on a link that leads out of the table editor while edits
 * are staged, before the router acts on it, and hands the address over so the
 * reader can be asked first.
 *
 * The App Router cannot be told to wait: once a link's own handler has run
 * the page is gone. A capture listener on the document runs before it. Links
 * that stay on this page — another table, a foreign key — are not caught here:
 * they change the address, and the editor asks when it sees that.
 */
export function useLinkGuard(active: boolean, onLeave: (href: string) => void) {
  useEffect(() => {
    if (!active) return
    const intercept = (event: MouseEvent) => {
      if (event.defaultPrevented || event.button !== 0) return
      if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
      const target = event.target instanceof Element ? event.target.closest("a[href]") : null
      if (!(target instanceof HTMLAnchorElement)) return
      if (target.target && target.target !== "_self") return
      if (target.hasAttribute("download")) return
      const url = new URL(target.href, window.location.href)
      if (url.origin !== window.location.origin) return
      if (url.pathname === window.location.pathname) return
      event.preventDefault()
      event.stopPropagation()
      onLeave(`${url.pathname}${url.search}${url.hash}`)
    }
    document.addEventListener("click", intercept, true)
    return () => document.removeEventListener("click", intercept, true)
  }, [active, onLeave])
}
