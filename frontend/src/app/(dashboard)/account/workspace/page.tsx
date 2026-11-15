"use client"

import { useState } from "react"
import Link from "next/link"
import { GridSquare } from "@/components/icons"
import { PURPOSES, purposeShown } from "@/lib/sidebar-sections"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { useHiddenSections } from "@/hooks/use-hidden-sections"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { hideableSections } from "@/components/nav"
import { Page, PageContext } from "@/components/page"
import { PurposePicker, SidebarEditor, useSectionCount } from "@/components/account/workspace"
import { SaveBarProvider, useSaveBarEntry } from "@/components/deploy/settings/save-bar"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { EditedTag, SettingsColumn } from "@/components/settings-column"
import { Button } from "@/components/ui/button"

/**
 * The first-run answer, after the first run: what this account uses the
 * server for, as the question's purposes, over the rail's own entries with a
 * switch on every one it may hide. The cards are the coarse answer and the
 * switches the fine one; both edit one draft, so pressing a purpose throws
 * its switches and throwing a switch can leave a purpose partly shown.
 *
 * It opens on one line that says where the answer stands — how many of the
 * sections the role may hide are shown, and how many purposes that makes —
 * with *Ask me again* at its end, and then it is a settings column, as a
 * project's settings are: the purposes as a grid of choices, the sidebar as
 * full-width switch rows on hairlines. They were two columns side by side,
 * the rail in a framed box. An edit raises the settings pages' one Save at
 * the foot of the window rather than a disabled button sitting under the
 * page for as long as nothing has changed.
 */
export default function AccountWorkspacePage() {
  return (
    <SaveBarProvider>
      <Workspace />
    </SaveBarProvider>
  )
}

function Workspace() {
  const { can } = useAuth()
  const { hidden, save } = useHiddenSections()
  const [draft, setDraft] = useState<Set<string> | null>(null)
  const current = draft ?? hidden
  const changes = draft
    ? [...draft].filter((key) => !hidden.has(key)).length +
      [...hidden].filter((key) => !draft.has(key)).length
    : 0
  const { shown, total } = useSectionCount(current)
  const offered = new Set<string>(
    hideableSections(can).flatMap((group) => group.sections.map(({ key }) => key)),
  )
  const purposes = PURPOSES.filter((purpose) => purposeShown(purpose.key, current, offered)).length
  const edited = changes > 0 ? <EditedTag /> : undefined

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
      <HostIdentity
        fallback={GridSquare}
        title="Your sidebar"
        facts={
          <>
            <span className="numeric">
              {shown} of {total} shown
            </span>
            <FactDot />
            <span className="numeric">
              {purposes === PURPOSES.length
                ? `${purposes} purposes`
                : `${purposes} of ${PURPOSES.length} purposes`}
            </span>
          </>
        }
        command={
          // The question again, on its own screen, for somebody whose use
          // of the server changed rather than one section of it.
          <Button variant="outline" size="sm" asChild>
            <Link href="/welcome">Ask me again</Link>
          </Button>
        }
      />
      <SettingsColumn>
        <SettingSection title="What you use this server for">
          <PurposePicker hidden={current} onChange={setDraft} />
        </SettingSection>
        {/* The switches are what is stored, so theirs is the head that says so. */}
        <SettingSection title="Sidebar" status={edited}>
          <SidebarEditor hidden={current} onChange={setDraft} />
        </SettingSection>
      </SettingsColumn>
    </Page>
  )
}
