"use client"

import { useState } from "react"
import Link from "next/link"
import { notify } from "@/lib/toast"
import { useHiddenSections } from "@/hooks/use-hidden-sections"
import { Page, PageContext, Section } from "@/components/page"
import { PurposePicker, SidebarEditor, useSectionCount } from "@/components/account/workspace"
import { SaveBarProvider, useSaveBarEntry } from "@/components/deploy/settings/save-bar"
import { Button } from "@/components/ui/button"

/**
 * The first-run answer, after the first run: what this account uses the
 * server for, as the question's purposes, beside the rail itself with a
 * switch on every row it may hide. The cards are the coarse answer and the
 * switches the fine one; both edit one draft, so pressing a purpose throws
 * its switches and throwing a switch can leave a purpose partly shown.
 *
 * A reading of the account's own setting that happens to be editable, so it
 * is drawn in the reading register — the welcome screen is the flow. Nothing
 * on it is framed but the rail, which is a picture of one. An edit raises
 * the settings pages' one Save at the foot of the window rather than a
 * disabled button sitting under the page for as long as nothing has changed.
 */
export default function AccountWorkspacePage() {
  return (
    <SaveBarProvider>
      <Workspace />
    </SaveBarProvider>
  )
}

function Workspace() {
  const { hidden, save } = useHiddenSections()
  const [draft, setDraft] = useState<Set<string> | null>(null)
  const current = draft ?? hidden
  const changes = draft
    ? [...draft].filter((key) => !hidden.has(key)).length +
      [...hidden].filter((key) => !draft.has(key)).length
    : 0
  const { shown, total } = useSectionCount(current)

  useSaveBarEntry(
    true,
    {
      name: "Sidebar",
      dirty: changes > 0,
      changes,
      saving: false,
      invalid: false,
      applies: "immediately",
    },
    {
      save: async () => {
        try {
          await save([...current])
          setDraft(null)
          return true
        } catch (err) {
          notify.error("Could not save your sidebar", err)
          return false
        }
      },
      discard: () => setDraft(null),
    },
  )

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Account" title="Workspace" />
      <div className="grid min-w-0 items-start gap-8 xl:grid-cols-[minmax(0,1fr)_20rem]">
        <Section
          title="What you use this server for"
          actions={
            // The question again, on its own screen, for somebody whose use
            // of the server changed rather than one section of it.
            <Button variant="outline" size="sm" asChild>
              <Link href="/welcome">Ask me again</Link>
            </Button>
          }
        >
          <PurposePicker hidden={current} onChange={setDraft} />
        </Section>
        <Section
          title="Sidebar"
          actions={
            // As tall as Ask me again, so the rail starts level with the cards.
            <span className="numeric flex h-8 items-center text-hint text-muted-foreground">
              {shown} of {total} shown
            </span>
          }
        >
          <SidebarEditor hidden={current} onChange={setDraft} />
        </Section>
      </div>
    </Page>
  )
}
