"use client"

import { useEffect, useRef, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import {
  Archive,
  ArrowRight,
  Bell,
  CodeBracket,
  Database,
  FirewallCheck,
  Gauge,
  Information,
  Layers,
  LockClosed,
  LockOpen,
  Question,
  Rocket,
  Servers,
  ShieldCheck,
  Sparkles,
  Terminal,
  Warning,
  type Icon,
} from "@/components/icons"
import { get, put } from "@/lib/api"
import { calendarDate, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { DeploymentSummary } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { ExcalidrawGlyph } from "@/components/boards/glyphs"
import { ProjectMark } from "@/components/deploy/project-mark"
import { Page, PageContext } from "@/components/page"
import { tabClasses } from "@/components/tabs"
import { ProductLogo } from "@/components/product-logo"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { FormSections, InfoTip } from "@/components/form"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { FactDot, HostFact } from "@/components/metrics/host-identity"
import { Button } from "@/components/ui/button"
import { SpotlightBorder } from "@/components/ui/spotlight-border"
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
import { AssistantOff } from "@/components/assistant/off"
import { AgentChat, type AgentSuggestion } from "@/components/assistant/agent/chat"
import { AgentChatList } from "@/components/assistant/agent/chat-list"
import { useAgentChats } from "@/components/assistant/agent/chats"
import { AgentWorkspace } from "@/components/assistant/agent/workspace"
import { readAbout } from "@/components/assistant/agent/about"
import {
  agentChatHref,
  agentChatId,
  agentModelChanged,
  newAgentChat,
  openAgentChat,
  useAgentSession,
  type AgentScope,
} from "@/components/assistant/agent/sessions"

const MODES: { mode: AssistantMode; icon: Icon }[] = [
  { mode: "ask", icon: LockClosed },
  { mode: "changes", icon: ShieldCheck },
  { mode: "full", icon: LockOpen },
]

const PLACES: { title: string; detail: string; href: string; icon: Icon }[] = [
  {
    title: "The whole server",
    detail: "Ask about anything on it, from this page's own chat",
    href: "/assistant",
    icon: Servers,
  },
  {
    title: "Ask from any page",
    detail: "Ask beside Search begins a chat about the page you are on",
    href: "/assistant",
    icon: Question,
  },
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
    title: "Databases",
    detail: "Each connection's own Assistant: what it holds, slow queries, access, backups",
    href: "/databases",
    icon: Database,
  },
  {
    title: "SQL editor",
    detail: "The same chat beside the editor, with the statement or a result attached",
    href: "/databases",
    icon: CodeBracket,
  },
  {
    title: "Commands on the host",
    detail: "Runs a shell command as the terminal's account, after asking unless on full access",
    href: "/terminal",
    icon: Terminal,
  },
  {
    title: "Excalidraw",
    detail: "Describe a diagram and have it drawn",
    href: "/excalidraw",
    icon: ExcalidrawGlyph,
  },
]

const SERVER: AgentScope = { kind: "server", name: "This server" }

/** What a chat begun from a page by Ask offers first: the page, not the server. */
const ABOUT_SUGGESTIONS: AgentSuggestion[] = [
  {
    label: "What am I looking at?",
    question: "What am I looking at on this page, and what does it tell me?",
    icon: Information,
  },
  {
    label: "Is anything wrong here?",
    question: "Is anything wrong with what this page shows, and what should I do about it?",
    icon: Warning,
  },
  {
    label: "What can I do from here?",
    question: "What can I do from this page, and what would you do next?",
    icon: Sparkles,
  },
]

const SERVER_SUGGESTIONS: AgentSuggestion[] = [
  {
    label: "How is the server doing?",
    question:
      "How is this server doing right now: CPU, memory, disk and load, and is anything unhealthy?",
    icon: Gauge,
  },
  {
    label: "Which containers are unhealthy?",
    question: "Which containers are stopped, restarting or unhealthy, and why?",
    product: "docker",
  },
  {
    label: "Anything wrong with the deployments?",
    question: "Is anything wrong with any deployment: failed runs, failing checks, errors?",
    icon: Rocket,
  },
  {
    label: "What failed lately?",
    question: "What failed on this server in the last day: runs, backups, services and alerts?",
    icon: Bell,
  },
  {
    label: "Certificates expiring?",
    question: "Which certificates expire in the next month, and will they renew on their own?",
    product: "caddy",
  },
  {
    label: "Check the firewall",
    question: "Read the firewall rules and tell me what is open to the internet and why.",
    icon: FirewallCheck,
  },
  {
    label: "Security updates",
    question: "Which security updates are waiting, and which of them need a restart?",
    icon: ShieldCheck,
  },
  {
    label: "What is using the disk?",
    question: "What is using the most disk space, and what can be cleaned up safely?",
    icon: Archive,
  },
]

/**
 * The assistant's own page. In front, a chat about the whole server, with every
 * chat the person has had in its history under what each was about: a deployment's
 * opens on that deployment's page, ready to carry on. Behind a second tab, who
 * answers — for the whole install, set up once here — how much it may do, and where
 * it works. Every place that uses it reads what is set there, and a place added
 * later does too.
 */
export function AssistantPage() {
  const { can } = useAuth()
  const settings = useAssistantSettings(true)
  const searchParams = useSearchParams()
  const view = settings.data
  // The chat needs a provider and the role that acts; until then the settings are
  // the page.
  const chatting =
    searchParams.get("view") !== "settings" && can("service.control") && view?.configured !== false

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
  return (
    <Page
      fill={chatting}
      className={cn(chatting ? "gap-0 pt-0 pb-0 md:gap-0 md:pt-0 md:pb-0" : "pt-0 md:pt-0")}
    >
      <PageContext title="Assistant" />
      <nav
        aria-label="The assistant's views"
        data-slot="assistant-tabs"
        className="flex shrink-0 items-end gap-1 border-b border-hairline pt-2 md:pt-3"
      >
        {can("service.control") && (
          <Link
            href="/assistant"
            replace
            aria-current={chatting ? "page" : undefined}
            className={tabClasses(chatting, "h-10")}
          >
            <Sparkles className="size-3.5" />
            Chat
          </Link>
        )}
        <Link
          href="/assistant?view=settings"
          replace
          aria-current={!chatting ? "page" : undefined}
          className={tabClasses(!chatting, "h-10")}
        >
          Settings
        </Link>
      </nav>
      {chatting ? (
        <ServerChat settings={settings} />
      ) : (
        <AssistantSettingsView settings={settings} />
      )}
    </Page>
  )
}

/** A chat about the whole server, and beside it every chat the person has had. */
function ServerChat({ settings }: { settings: ReturnType<typeof useAssistantSettings> }) {
  const router = useRouter()
  const searchParams = useSearchParams()
  const { confirm, dialog } = useConfirm()
  const [settingUp, setSettingUp] = useState(false)
  const { can } = useAuth()
  const view = settings.data!
  const session = useAgentSession(SERVER)
  const chats = useAgentChats({}, view.configured)

  const wanted = searchParams.get("chat")
  const opening = useRef("")
  useEffect(() => {
    if (!wanted) opening.current = ""
    if (!wanted || !view.configured || opening.current === wanted) return
    opening.current = wanted
    void openAgentChat(SERVER, wanted)
  }, [wanted, view.configured])

  // Ask from another page arrives naming it: a new chat about that page, begun
  // once, and the address let go of so a reload does not begin another.
  const aboutPath = searchParams.get("about")
  const aboutTitle = searchParams.get("title")
  const begun = useRef("")
  useEffect(() => {
    if (!aboutPath) {
      begun.current = ""
      return
    }
    if (!view.configured || begun.current === aboutPath) return
    begun.current = aboutPath
    const about = readAbout({
      get: (name) => (name === "about" ? aboutPath : name === "title" ? aboutTitle : null),
    })
    if (about) newAgentChat(SERVER, about)
    router.replace("/assistant", { scroll: false })
  }, [aboutPath, aboutTitle, view.configured, router])

  const startNew = () => {
    newAgentChat(SERVER)
    if (wanted) router.replace("/assistant", { scroll: false })
  }
  const product = assistantProduct(view)
  if (!view.configured) {
    return (
      <>
        <AssistantOff
          className="m-auto"
          settings={view}
          onSetUp={() => setSettingUp(true)}
          what="it can read the whole server, answer anything about it, and change it for you."
        />
        {can("system.admin") && (
          <AssistantSettingsSheet
            open={settingUp}
            settings={view}
            onClose={() => setSettingUp(false)}
            onSaved={settings.refresh}
            confirm={confirm}
          />
        )}
        {dialog}
      </>
    )
  }
  return (
    <AgentWorkspace
      session={session}
      product={product}
      chat={
        <AgentChat
          key={session.key}
          session={session}
          settings={view}
          title={
            session.transcript.about
              ? `Ask about ${session.transcript.about.title}`
              : "Ask anything about this server"
          }
          description="It can read the whole server — containers, deployments, services, logs, certificates and the firewall — and change what you ask it to, as you and under your role."
          placeholder={
            session.transcript.about
              ? `Ask about ${session.transcript.about.title}…`
              : "Ask anything about this server…"
          }
          suggestions={session.transcript.about ? ABOUT_SUGGESTIONS : SERVER_SUGGESTIONS}
          sends="what it reads of the server to answer"
          onNew={startNew}
        />
      }
      chats={
        <AgentChatList
          grouped
          chats={chats.data}
          error={chats.error}
          activeId={agentChatId(session)}
          onNew={startNew}
          onDeleted={(chat) => {
            if (chat.id === wanted) router.replace("/assistant", { scroll: false })
          }}
          onOpen={(chat) => {
            if (chat.id === agentChatId(session)) return
            const href = agentChatHref(chat)
            if (chat.scope.kind === "server") router.replace(href, { scroll: false })
            else router.push(href)
          }}
        />
      }
    />
  )
}

/**
 * Who answers, how much it may do, and where it works: the install's settings,
 * as one column of sections on hairlines, the shape a project's settings have.
 * They were a bordered card over four framed panels, two of them side by side.
 *
 * Nothing here waits for a save bar: a provider is saved in its sheet and a
 * mode is the install's the moment it is pressed, so each control says its
 * own outcome.
 */
function AssistantSettingsView({
  settings,
}: {
  settings: ReturnType<typeof useAssistantSettings>
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState<{ preset?: string } | null>(null)
  // The mode being written: a second press waits for the first.
  const [choosing, setChoosing] = useState<AssistantMode>()
  const view = settings.data!
  const deployments = usePoll<DeploymentSummary[]>(
    (signal) => get<DeploymentSummary[]>("/deploy/", undefined, signal),
    0,
    [],
    { enabled: can("service.control") },
  )

  const mode = view.defaultMode ?? "changes"
  const setMode = async (defaultMode: AssistantMode) => {
    if (choosing || defaultMode === mode) return
    setChoosing(defaultMode)
    try {
      await put("/assistant/preferences", { defaultMode })
      announceAssistantChange()
      notify.success(`Conversations now start on ${MODE_LABEL[defaultMode]}`)
    } catch (err) {
      notify.error("The default could not be changed", err instanceof Error ? err : undefined)
    } finally {
      setChoosing(undefined)
    }
  }
  const profiles = view.profiles ?? []
  const presets = view.presets ?? []
  const profileOf = (preset: AssistantPreset) =>
    profiles.find((entry) => entry.preset === preset.id)
  const saved = presets.filter(profileOf)
  const unsaved = presets.filter((preset) => !profileOf(preset))
  const provider = presets.find((preset) => preset.id === view.preset)
  const projects = deployments.data ?? []

  return (
    <>
      <div className="mx-auto w-full max-w-3xl min-w-0 animate-rise pt-6 md:pt-8">
        <FormSections>
          <SettingSection
            id="assistant-model"
            title="Model"
            state={
              view.configured ? (
                <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                  <HostFact product={assistantProduct(view)}>
                    {provider?.label ?? view.provider}
                  </HostFact>
                  <FactDot />
                  <span className="font-mono text-foreground">{view.model}</span>
                  {view.updatedBy && (
                    <>
                      <FactDot />
                      <span>
                        set up by {view.updatedBy}
                        {view.updatedAt && ` on ${calendarDate(view.updatedAt)}`}
                      </span>
                    </>
                  )}
                </span>
              ) : (
                <span className="font-medium text-foreground">The assistant is off</span>
              )
            }
            actions={
              view.configured && (
                <ModelPicker
                  settings={view}
                  onChanged={agentModelChanged}
                  className="h-8 border border-hairline"
                />
              )
            }
          >
            {/* Off, the way to turn it on is the section's one command: under
                the words on a phone and at their full width, where beside
                them it squeezed the head into a column a word wide. */}
            {!view.configured && admin && (
              <Button onClick={() => setEditing({})} className="max-sm:w-full">
                <Sparkles />
                Set up the assistant
              </Button>
            )}
          </SettingSection>

          <SettingSection
            title="Providers"
            state={
              saved.length > 0
                ? `${saved.length} of ${presets.length} set up`
                : `${presets.length} to choose from`
            }
          >
            <ChoiceGrid columns={2} role="list" aria-label="Providers">
              {[...saved, ...unsaved].map((preset) => (
                <ProviderCard
                  key={preset.id}
                  preset={preset}
                  profile={profileOf(preset)}
                  admin={admin}
                  onEdit={() => setEditing({ preset: preset.id })}
                />
              ))}
            </ChoiceGrid>
          </SettingSection>

          <SettingSection
            title="Default mode"
            state={`Conversations start on ${MODE_LABEL[mode]}`}
            status={<InfoTip label="What holds in every mode">{MODES_NOTE}</InfoTip>}
          >
            <ChoiceGrid role="group" aria-label="Default mode">
              {MODES.map(({ mode: option, icon }, index) => (
                <ChoiceCard
                  key={option}
                  index={index}
                  verb={`${MODE_LABEL[option]}, for new conversations`}
                  title={MODE_LABEL[option]}
                  description={MODE_DETAIL[option]}
                  mark={icon}
                  selected={mode === option}
                  // A role that may not choose reads the choice and is given
                  // no control: the card it is on stays lit, the rest step
                  // back. One write at a time, so a second press waits.
                  disabled={!admin || (choosing !== undefined && choosing !== option)}
                  onClick={() => void setMode(option)}
                />
              ))}
            </ChoiceGrid>
          </SettingSection>

          <SettingSection title="Where it works" state={`${PLACES.length} places`}>
            <ul
              aria-label="Where it works"
              className="-mx-2 grid min-w-0 sm:grid-cols-2 sm:gap-x-6"
            >
              {PLACES.map((place) => (
                <li key={place.title} className="min-w-0">
                  <Link
                    href={place.href}
                    title={place.detail}
                    className="group flex h-14 min-w-0 items-center gap-3 rounded-lg px-2 focus-ring transition-colors hover:bg-row-hover"
                  >
                    <place.icon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-body font-medium">{place.title}</span>
                      <span className="block truncate text-hint text-muted-foreground">
                        {place.detail}
                      </span>
                    </span>
                    <ArrowRight
                      aria-hidden
                      className="size-3.5 shrink-0 text-muted-foreground transition-colors group-hover:text-foreground"
                    />
                  </Link>
                </li>
              ))}
            </ul>
          </SettingSection>

          {view.configured && projects.length > 0 && (
            <SettingSection
              title="Ask about a deployment"
              state={
                projects.length > SHOWN_PROJECTS
                  ? `${SHOWN_PROJECTS} of ${projects.length} projects`
                  : plural(projects.length, "project")
              }
            >
              <ChoiceList
                aria-label="Deployments to ask about"
                className="grid gap-2 space-y-0 sm:grid-cols-2"
              >
                {projects.slice(0, SHOWN_PROJECTS).map((deployment) => (
                  <ChoiceRow
                    key={deployment.id}
                    href={`/deploy/${deployment.id}/assistant`}
                    verb={`Ask about ${deployment.name}`}
                    leading={<ProjectMark deployment={deployment} size="sm" />}
                    title={deployment.name}
                  />
                ))}
              </ChoiceList>
            </SettingSection>
          )}
        </FormSections>
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
    </>
  )
}

/** As many projects as the section offers; the rest are on Deployments. */
const SHOWN_PROJECTS = 12

/**
 * What is true whichever mode is chosen, behind the head's mark rather than
 * under the cards, where it was a paragraph of four lines. The head says the
 * one thing the paragraph was for: which mode a conversation starts on.
 */
const MODES_NOTE =
  "Anyone asking can change the mode for their own conversation. In every mode it acts as the person asking and within their role, every call it makes is audited, and a person's own credentials (signing in, passkeys, sessions and API keys) stay out of its reach."

/**
 * One provider as a card you pick: its own logo, its name, and the model it
 * answers with once it is set up, or what it is until then. The one in use
 * carries its state as a dot and a word; an administrator's press opens its
 * setup. Every card is one height, set up or not.
 */
function ProviderCard({
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
        <span className="block truncate text-body font-medium">{preset.label}</span>
        <span
          className={cn("block truncate text-hint text-muted-foreground", profile && "font-mono")}
        >
          {profile ? (profile.modelLabel ?? profile.model) : preset.detail}
        </span>
      </span>
      {profile?.active ? (
        <Status tone="running" label="In use" />
      ) : profile ? (
        <Status tone="stopped" label="Saved" />
      ) : (
        admin && <span className="shrink-0 text-hint text-muted-foreground">Set up</span>
      )}
    </>
  )
  const shape = "flex h-16 w-full min-w-0 items-center gap-3 rounded-xl px-3 text-left"
  return (
    <div
      role="listitem"
      data-slot="assistant-provider"
      data-preset={preset.id}
      data-saved={Boolean(profile)}
      className="min-w-0"
    >
      {admin ? (
        <SpotlightBorder resting={profile?.active ? "brand" : "border"}>
          <button
            type="button"
            onClick={onEdit}
            aria-label={`${preset.label}: ${profile ? "edit" : "set up"}`}
            className={cn(shape, "focus-ring", profile?.active && "bg-accent")}
          >
            {inner}
          </button>
        </SpotlightBorder>
      ) : (
        <div
          className={cn(
            shape,
            "border bg-choice-surface",
            profile?.active && "border-brand bg-accent",
          )}
        >
          {inner}
        </div>
      )}
    </div>
  )
}
