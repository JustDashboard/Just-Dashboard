"use client"

import { useConfirm, type ConfirmRequest } from "@/components/confirm-dialog"
import { FormSections } from "@/components/form"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import { SectionFrame } from "@/components/database/kit"
import { BlockedState } from "@/components/database/kit/blocked-state"
import { useFocusReturn } from "@/components/database/redis/use-focus-return"
import { useDatabase } from "@/components/database/shell/database-context"
import { settingsPages, type SettingsPageKey } from "@/components/database/shell/settings-pages"

/** A page an engine lacks, as the end of "<Engine> has no …". */
const MISSING: Record<SettingsPageKey, string> = {
  general: "settings",
  network: "network to configure",
  resources: "resources to limit",
  pooling: "connection pooling",
  parameters: "parameters to change here",
  extensions: "extensions",
  version: "release to upgrade from here",
  danger: "danger zone",
}

/**
 * One page of a database's settings, in the shape every settings page in the
 * product has (`deploy/settings/setting-card.tsx`): one centred column of
 * sections divided by hairlines, each head over its fields carrying what the
 * section currently is, and the page's one Save floating at its foot while an
 * edit is held.
 *
 * The pages are the rail's (`settingsPages`), so a page the engine has no use
 * for is not listed; reached by its address all the same, it says so in the
 * engine's own terms rather than drawing a form the server would refuse.
 *
 * Each page is a module of its own beside this one (`settings.tsx` is General,
 * `settings-<page>-page.tsx` the rest), and a route imports its page from that
 * module. They were eight exports of one file behind a folder index, and a
 * bundler keeps a module whole: the Version page shipped the forms of the
 * other seven, and of Access, Alerts and Backups besides.
 */
export function SettingsFrame({
  page,
  children,
}: {
  page: SettingsPageKey
  children: (confirm: (request: ConfirmRequest) => void) => React.ReactNode
}) {
  const { engine, href } = useDatabase()
  const { confirm, dialog } = useConfirm()
  // Every dialog here is opened by state: this hands the keyboard back to
  // the control that opened one.
  useFocusReturn()
  const listed = settingsPages(engine).find((one) => one.key === page)
  if (!listed) {
    return (
      <SectionFrame section="settings">
        <BlockedState
          engine={engine}
          thing={MISSING[page]}
          href={href("settings")}
          action="Open General"
        >
          {engine.can("fileBased")
            ? "It is a file on this machine: there is no server to reach, configure or share."
            : page === "version"
              ? "A PostgreSQL server Databases started is moved to a newer release here; this one is upgraded with its own tools."
              : "The engine keeps nothing of the kind for the dashboard to read or change."}
        </BlockedState>
      </SectionFrame>
    )
  }
  return (
    <SectionFrame section="settings" part={page === "general" ? undefined : listed.title}>
      <SaveBarProvider>
        {/* The room at the foot is the save bar's, so it never lies over the last field. */}
        <div className="mx-auto w-full max-w-3xl min-w-0 pb-20">
          <FormSections>{children(confirm)}</FormSections>
        </div>
      </SaveBarProvider>
      {dialog}
    </SectionFrame>
  )
}
