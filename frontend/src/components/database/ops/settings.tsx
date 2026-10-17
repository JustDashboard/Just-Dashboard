"use client"

import { useEffect } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm, type ConfirmRequest } from "@/components/confirm-dialog"
import { FormSections } from "@/components/form"
import { EmptyState } from "@/components/state"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import { SectionFrame } from "@/components/database/kit"
import { BlockedState } from "@/components/database/kit/blocked-state"
import { EngineMark } from "@/components/database/kit/engine-mark"
import { AddressSection, ConnectionForm } from "@/components/database/ops/settings-connection"
import { DangerSection } from "@/components/database/ops/settings-danger"
import { ExtensionsSection } from "@/components/database/ops/settings-extensions"
import { ParametersSection } from "@/components/database/ops/settings-parameters"
import { PoolingSections } from "@/components/database/ops/settings-pooling"
import { ReachabilitySection } from "@/components/database/ops/settings-reach"
import { ResourcesSection } from "@/components/database/ops/settings-resources"
import { useFocusReturn } from "@/components/database/redis/use-focus-return"
import { useDatabase } from "@/components/database/shell/database-context"
import {
  formerSettingsPage,
  settingsPageHref,
  settingsPages,
  type SettingsPageKey,
} from "@/components/database/shell/settings-pages"

/** A page an engine lacks, as the end of "<Engine> has no …". */
const MISSING: Record<SettingsPageKey, string> = {
  general: "settings",
  network: "network to configure",
  resources: "resources to limit",
  pooling: "connection pooling",
  parameters: "parameters to change here",
  extensions: "extensions",
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
 */
function SettingsFrame({
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

/**
 * General: the address the dashboard dials, drawn as the way it goes, then
 * what it calls the connection and whether it may change what is in it.
 *
 * It is also where an address from the one-page Settings lands: `#danger`,
 * `#reachability` or a parameter search (`?q=`) is sent on, in place, to the
 * page that holds it now, and `#databases` to Home, whose list of what else
 * the server holds is the one that section was.
 */
export function Settings() {
  const { href } = useDatabase()
  const router = useRouter()
  const search = useSearchParams()
  useEffect(() => {
    if (window.location.hash === "#databases") {
      router.replace(href("home"))
      return
    }
    const page = formerSettingsPage(window.location.hash, search)
    if (!page) return
    const query = page === "parameters" ? search.toString() : ""
    const to = settingsPageHref(href("settings"), { path: page })
    router.replace(query ? `${to}?${query}` : to)
  }, [href, router, search])
  return (
    <SettingsFrame page="general">
      {() => (
        <>
          <AddressSection />
          <ConnectionForm />
        </>
      )}
    </SettingsFrame>
  )
}

export function SettingsNetwork() {
  return (
    <SettingsFrame page="network">
      {(confirm) => <ReachabilitySection confirm={confirm} />}
    </SettingsFrame>
  )
}

export function SettingsResources() {
  return (
    <SettingsFrame page="resources">
      {(confirm) => <ResourcesSection confirm={confirm} />}
    </SettingsFrame>
  )
}

export function SettingsPooling() {
  return (
    <SettingsFrame page="pooling">
      {(confirm) => <PoolingSections confirm={confirm} />}
    </SettingsFrame>
  )
}

export function SettingsParameters() {
  return <SettingsFrame page="parameters">{() => <ParametersSection />}</SettingsFrame>
}

export function SettingsExtensions() {
  return (
    <SettingsFrame page="extensions">
      {(confirm) => <ExtensionsSection confirm={confirm} />}
    </SettingsFrame>
  )
}

/**
 * The acts that end it. They are an administrator's who may destroy; any
 * other role reaching the address is told whose they are, with nothing to
 * press.
 */
export function SettingsDanger() {
  const { engine } = useDatabase()
  const { can } = useAuth()
  const mayDestroy = can("system.admin") && can("destructive")
  return (
    <SettingsFrame page="danger">
      {(confirm) =>
        mayDestroy ? (
          <DangerSection confirm={confirm} />
        ) : (
          <EmptyState
            mark={<EngineMark engine={engine} />}
            title="The danger zone is an administrator's"
            description="Forgetting the connection, deleting the database and removing its container are taken by an administrator whose role may destroy."
          />
        )
      }
    </SettingsFrame>
  )
}
