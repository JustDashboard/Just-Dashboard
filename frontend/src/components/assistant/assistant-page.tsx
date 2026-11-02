"use client"

import { useState } from "react"
import Link from "next/link"
import {
  ArrowRight,
  Check,
  Database,
  Layers,
  LockClosed,
  LockOpen,
  Rocket,
  ShieldCheck,
  Sparkles,
  type Icon,
} from "@/components/icons"
import { get, put } from "@/lib/api"
import { calendarDate } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentSummary } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { ExcalidrawGlyph } from "@/components/boards/glyphs"
import { ProjectMark } from "@/components/deploy/project-mark"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { AssistantMark } from "@/components/assistant/mark"
import { ModelPicker } from "@/components/assistant/model-picker"
import { AssistantSettingsSheet } from "@/components/assistant/settings-sheet"
import {
  MODE_DETAIL,
  MODE_LABEL,
  announceAssistantChange,
  assistantProduct,
  useAssistantSettings,
  type AssistantMode,
  type AssistantPreset,
  type AssistantProfile,
} from "@/components/assistant/settings"
import { agentModelChanged } from "@/components/assistant/agent/sessions"

const MODES: { mode: AssistantMode; icon: Icon }[] = [
  { mode: "ask", icon: LockClosed },
  { mode: "changes", icon: ShieldCheck },
  { mode: "full", icon: LockOpen },
]

const PLACES: { title: string; detail: string; href: string; icon: Icon }[] = [
  {
    title: "Deployments",
    detail: "Ask anything, read logs and requests, change settings, take backups",
    href: "/deploy",
    icon: Rocket,
  },
  {
    title: "New deployments",
    detail: "Import a repository and let it choose the settings",
    href: "/deploy/new",
    icon: Layers,
  },
  {
    title: "SQL editor",
    detail: "Write and explain statements against a database's structure",
    href: "/databases",
    icon: Database,
  },
  {
    title: "Excalidraw",
    detail: "Describe a diagram and have it drawn",
    href: "/excalidraw",
    icon: ExcalidrawGlyph,
  },
]

/**
 * The assistant's own page: who answers, for the whole install, set up once here;
 * how much it may do; and where it works. Every place that uses it reads what is
 * set here, and a place added later does too.
 */
export function AssistantPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const settings = useAssistantSettings(true)
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState<{ preset?: string } | null>(null)
  const view = settings.data
  const deployments = usePoll<DeploymentSummary[]>(
    (signal) => get<DeploymentSummary[]>("/deploy/", undefined, signal),
    0,
    [],
    { enabled: can("service.control") },
  )

  if (!view) {
    return (
      <Page>
        <PageContext title="Assistant" />
        {settings.error ? (
          <ErrorState error={settings.error} onRetry={settings.refresh} />
        ) : (
          <LoadingPanel />
        )}
      </Page>
    )
  }

  const setMode = async (defaultMode: AssistantMode) => {
    try {
      await put("/assistant/preferences", { defaultMode })
      announceAssistantChange()
      notify.success(`Conversations now start on ${MODE_LABEL[defaultMode]}`)
    } catch (err) {
      notify.error("The default could not be changed", err instanceof Error ? err : undefined)
    }
  }
  const profiles = view.profiles ?? []
  const presets = view.presets ?? []
  const profileOf = (preset: AssistantPreset) =>
    profiles.find((entry) => entry.preset === preset.id)
  const saved = presets.filter(profileOf)
  const unsaved = presets.filter((preset) => !profileOf(preset))

  return (
    <Page className="animate-rise">
      <PageContext title="Assistant" />
      <div className="space-y-6">
        <section
          data-slot="assistant-now"
          className="flex min-w-0 flex-wrap items-center gap-4 rounded-xl border border-hairline bg-card p-4"
        >
          <AssistantMark product={assistantProduct(view)} size="md" />
          <div className="min-w-0 flex-1">
            <p className="text-lg font-semibold tracking-tight">
              {view.configured ? (view.modelLabel ?? view.model) : "The assistant is off"}
            </p>
            <p className="text-body text-muted-foreground">
              {view.configured
                ? `Every place in the dashboard asks this model${view.updatedBy ? `, set up by ${view.updatedBy}` : ""}${view.updatedAt ? ` on ${calendarDate(view.updatedAt)}` : ""}.`
                : "Choose who answers it and it works everywhere at once."}
            </p>
          </div>
          {view.configured ? (
            <ModelPicker
              settings={view}
              onChanged={agentModelChanged}
              className="h-9 border border-hairline"
            />
          ) : (
            admin && (
              <Button onClick={() => setEditing({})}>
                <Sparkles />
                Set up the assistant
              </Button>
            )
          )}
        </section>

        <Panel>
          <PanelHeader title="Providers" />
          <PanelBody>
            <ul className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
              {saved.map((preset) => (
                <ProviderRow
                  key={preset.id}
                  preset={preset}
                  profile={profileOf(preset)}
                  admin={admin}
                  onEdit={() => setEditing({ preset: preset.id })}
                />
              ))}
              {unsaved.map((preset) => (
                <ProviderRow
                  key={preset.id}
                  preset={preset}
                  admin={admin}
                  onEdit={() => setEditing({ preset: preset.id })}
                />
              ))}
            </ul>
          </PanelBody>
        </Panel>

        <div className="grid gap-6 lg:grid-cols-2">
          <Panel>
            <PanelHeader title="What it may do on its own" />
            <PanelBody>
              <ul className="space-y-1.5">
                {MODES.map(({ mode, icon: Mark }) => {
                  const chosen = (view.defaultMode ?? "changes") === mode
                  return (
                    <li key={mode}>
                      <button
                        type="button"
                        disabled={!admin}
                        aria-pressed={chosen}
                        onClick={() => void setMode(mode)}
                        className={cn(
                          "flex w-full min-w-0 items-start gap-3 rounded-lg border p-3 text-left focus-ring transition-colors",
                          chosen
                            ? "border-rule-brand bg-wash-brand"
                            : "border-hairline enabled:hover:bg-row-hover",
                        )}
                      >
                        <Mark
                          aria-hidden
                          className={cn(
                            "mt-0.5 size-4 shrink-0",
                            mode === "full" ? "text-warning" : "text-muted-foreground",
                          )}
                        />
                        <span className="min-w-0 flex-1">
                          <span className="block text-body font-medium">{MODE_LABEL[mode]}</span>
                          <span className="block text-hint leading-relaxed text-muted-foreground">
                            {MODE_DETAIL[mode]}
                          </span>
                        </span>
                        {chosen && <Check aria-hidden className="mt-0.5 size-4 shrink-0" />}
                      </button>
                    </li>
                  )
                })}
              </ul>
              <p className="mt-3 text-hint leading-relaxed text-muted-foreground">
                This is what a conversation starts on; anyone asking can change it for theirs. What
                stays with a person in every mode: reading a secret&rsquo;s value, saved
                credentials, typed deletions, merging a pull request and running its preview.
              </p>
            </PanelBody>
          </Panel>
          <Panel>
            <PanelHeader title="Where it works" />
            <PanelBody flush>
              <ul className="divide-y divide-hairline">
                {PLACES.map((place) => (
                  <li key={place.href}>
                    <Link
                      href={place.href}
                      className="group flex min-w-0 items-center gap-3 px-4 py-3 focus-ring transition-colors hover:bg-row-hover"
                    >
                      <place.icon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1">
                        <span className="block text-body font-medium">{place.title}</span>
                        <span className="block truncate text-hint text-muted-foreground">
                          {place.detail}
                        </span>
                      </span>
                      <ArrowRight aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
                    </Link>
                  </li>
                ))}
              </ul>
            </PanelBody>
          </Panel>
        </div>

        {view.configured && (deployments.data?.length ?? 0) > 0 && (
          <Panel>
            <PanelHeader title="Ask about a deployment" />
            <PanelBody>
              <ul className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
                {deployments.data!.slice(0, 12).map((deployment) => (
                  <li key={deployment.id}>
                    <Link
                      href={`/deploy/${deployment.id}/assistant`}
                      className="flex min-w-0 items-center gap-3 rounded-lg border border-hairline p-2.5 focus-ring transition-colors hover:bg-row-hover"
                    >
                      <ProjectMark deployment={deployment} size="sm" />
                      <span className="min-w-0 flex-1 truncate text-body font-medium">
                        {deployment.name}
                      </span>
                      <ArrowRight aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
                    </Link>
                  </li>
                ))}
              </ul>
            </PanelBody>
          </Panel>
        )}
      </div>
      {admin && (
        <AssistantSettingsSheet
          open={editing !== null}
          settings={view}
          preset={editing?.preset}
          onClose={() => setEditing(null)}
          onSaved={settings.refresh}
          confirm={confirm}
        />
      )}
      {dialog}
    </Page>
  )
}

function ProviderRow({
  preset,
  profile,
  admin,
  onEdit,
}: {
  preset: AssistantPreset
  profile?: AssistantProfile
  admin: boolean
  onEdit: () => void
}) {
  const inner = (
    <>
      <ProductLogo id={preset.product || undefined} size="md" fallback={Sparkles} />
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate text-body font-medium">{preset.label}</span>
          {profile?.active && <Tag tone="success">In use</Tag>}
        </span>
        <span className="block truncate text-hint text-muted-foreground">
          {profile ? (profile.modelLabel ?? profile.model) : preset.detail}
        </span>
      </span>
      {admin && !profile && (
        <span className="shrink-0 text-hint text-muted-foreground">Set up</span>
      )}
    </>
  )
  const frame = cn(
    "flex w-full min-w-0 items-center gap-3 rounded-lg border p-2.5 text-left",
    profile?.active ? "border-rule-success bg-wash-success" : "border-hairline",
  )
  return (
    <li data-slot="assistant-provider" data-preset={preset.id} data-saved={Boolean(profile)}>
      {admin ? (
        <button
          type="button"
          onClick={onEdit}
          aria-label={`${preset.label}: ${profile ? "edit" : "set up"}`}
          className={cn(frame, "focus-ring transition-colors hover:bg-row-hover")}
        >
          {inner}
        </button>
      ) : (
        <div className={frame}>{inner}</div>
      )}
    </li>
  )
}
