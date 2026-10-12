"use client"

import { useState } from "react"
import Link from "next/link"
import { notify } from "@/lib/toast"
import { useHiddenSections } from "@/hooks/use-hidden-sections"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import {
  SectionSwitches,
  SidebarPreview,
  useSectionCount,
} from "@/components/onboarding/section-switches"
import { Button } from "@/components/ui/button"

/**
 * The first-run answer, after the first run: which sections this account
 * keeps in its sidebar, as the same switches beside the same preview, and a
 * way to be asked the question again.
 *
 * A reading of the account's own setting that happens to be editable, so it
 * is drawn in the reading register — the welcome screen is the flow.
 */
export default function AccountWorkspacePage() {
  const { hidden, save } = useHiddenSections()
  const [draft, setDraft] = useState<Set<string> | null>(null)
  const [saving, setSaving] = useState(false)
  const current = draft ?? hidden
  const dirty =
    draft !== null && (draft.size !== hidden.size || [...draft].some((key) => !hidden.has(key)))
  const { shown, total } = useSectionCount(current)

  const commit = async () => {
    setSaving(true)
    try {
      await save([...current])
      setDraft(null)
      notify.success("Sidebar saved")
    } catch (err) {
      notify.error("Could not save your sidebar", err)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Account" title="Workspace" />
      <div className="grid min-w-0 items-start gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <Panel>
          <PanelHeader
            title="Sidebar sections"
            actions={
              <>
                <span className="numeric mr-2 text-hint text-muted-foreground">
                  {shown} of {total} shown
                </span>
                {/* The question again, for somebody whose use of the server
                    changed rather than one section of it. */}
                <Button variant="outline" size="sm" asChild>
                  <Link href="/welcome">Ask me again</Link>
                </Button>
              </>
            }
          />
          <PanelBody>
            <SectionSwitches hidden={current} onChange={setDraft} />
          </PanelBody>
          <PanelFooter>
            <p className="mr-auto min-w-0 text-hint text-muted-foreground">
              Hiding a section changes your sidebar, not what you can do: its pages still open from
              links and search.
            </p>
            {dirty && (
              <Button size="sm" variant="ghost" onClick={() => setDraft(null)} disabled={saving}>
                Discard
              </Button>
            )}
            <Button size="sm" onClick={commit} disabled={!dirty || saving} pending={saving}>
              Save changes
            </Button>
          </PanelFooter>
        </Panel>
        <div className="min-w-0 space-y-2">
          <p className="eyebrow px-1">Your sidebar</p>
          <SidebarPreview hidden={current} />
        </div>
      </div>
    </Page>
  )
}
