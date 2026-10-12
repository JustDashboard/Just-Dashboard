"use client"

import { useEffect, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import {
  Box,
  Database,
  Link,
  Logout,
  MagnifyingGlass,
  Rocket,
  SettingsSliders,
  Shield,
  Terminal,
  Topology,
  type Icon,
} from "@/components/icons"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DbConnection, DeploymentFleet } from "@/lib/types"
import { PURPOSES, hiddenFor, type PurposeKey, type SectionKey } from "@/lib/sidebar-sections"
import { useAuth } from "@/hooks/use-auth"
import { useHiddenSections } from "@/hooks/use-hidden-sections"
import { usePoll } from "@/hooks/use-poll"
import { Page } from "@/components/page"
import {
  FlowActions,
  FlowHeader,
  FlowPanel,
  FlowPanelBody,
  FlowPanelHeader,
  FlowSteps,
} from "@/components/flow"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { sectionEntry } from "@/components/nav"
import { displayNameOf } from "@/components/account/user-avatar"
import { useLauncherKeys } from "@/components/command-search/keycaps"
import { Logo } from "@/components/logo"
import {
  SectionSwitches,
  SidebarPreview,
  useSectionCount,
} from "@/components/onboarding/section-switches"
import { Button } from "@/components/ui/button"
import { Confetti, type ConfettiRef } from "@/components/ui/confetti"

const STEPS = [
  { key: "purpose", label: "Purpose" },
  { key: "sidebar", label: "Sidebar" },
  { key: "ready", label: "Ready" },
]

const QUESTIONS = [
  "What will you use this server for?",
  "Which sections should your sidebar show?",
  "Your sidebar is ready",
]

const PURPOSE_MARKS: Record<PurposeKey, Icon> = {
  ship: Rocket,
  databases: Database,
  containers: Box,
  network: Topology,
  secure: Shield,
  workspace: Terminal,
}

/**
 * The first thing a new account sees after signing in: what the server is
 * for, which sections that keeps in the sidebar, and what hiding one means.
 *
 * A flow page (§16): a sequence with an outcome, the account's sidebar. The
 * purposes are a first draft of the answer, not the answer — the second
 * screen is the switches themselves beside the rail they draw, so a choice
 * that picked Network for somebody who only wanted the proxy is undone in a
 * press rather than by finding Account → Workspace later. Nothing is saved
 * until Finish, or Show me everything, and either one is the end of the
 * question: the shell never sends this account here again.
 */
export function WelcomeFlow() {
  const router = useRouter()
  const { status, logout } = useAuth()
  const { save } = useHiddenSections()
  const [step, setStep] = useState(0)
  // Null until the reader presses a purpose: until then the choice is what
  // the server suggests. Held here so Back returns to the cards as they were.
  const [purposes, setPurposes] = useState<PurposeKey[] | null>(null)
  const [hidden, setHidden] = useState<Set<string>>(new Set())
  const [saving, setSaving] = useState<"finish" | "skip" | null>(null)
  const confetti = useRef<ConfettiRef>(null)
  const page = useRef<HTMLDivElement>(null)
  const name = status?.user ? displayNameOf(status.user) : ""

  // Each step is a new screen. Stacked on a phone the command is at the foot
  // of a long page, and the next question would otherwise open scrolled to
  // wherever the last one was pressed.
  useEffect(() => {
    page.current?.parentElement?.scrollTo({ top: 0 })
  }, [step])

  const commit = async (sections: Set<string>, how: "finish" | "skip") => {
    setSaving(how)
    try {
      await save([...sections])
      if (how === "skip") {
        router.replace("/")
        return
      }
      setStep(2)
      // The outcome landing in front of the reader, once (§17 pass 7).
      confetti.current?.fire()
    } catch (err) {
      notify.error("Could not save your sidebar", err)
    } finally {
      setSaving(null)
    }
  }

  return (
    <Page ref={page} register="flow" fill="xl" className="animate-rise">
      <Confetti ref={confetti} className="pointer-events-none fixed inset-0 z-50 size-full" />
      {/* There is no rail on this screen — the rail is what it decides — so
          the mark and the one way out that is not an answer sit above it. */}
      <div className="flex shrink-0 items-center justify-between gap-4">
        <Logo />
        <Button variant="ghost" size="sm" onClick={() => logout()}>
          <Logout className="size-3.5" />
          Sign out
        </Button>
      </div>
      <FlowHeader
        eyebrow={`Welcome${name ? `, ${name}` : ""} · Step ${step + 1} of ${STEPS.length}`}
        question={QUESTIONS[step]}
        steps={<FlowSteps steps={STEPS} current={step} />}
      />
      <div className="min-w-0 xl:min-h-0 xl:flex-1">
        {step === 0 && (
          <PurposeStep
            picked={purposes}
            onPick={setPurposes}
            skipping={saving === "skip"}
            onSkip={() => commit(new Set(), "skip")}
            onContinue={(chosen) => {
              setHidden(new Set(hiddenFor(chosen)))
              setStep(1)
            }}
          />
        )}
        {step === 1 && (
          <SidebarStep
            hidden={hidden}
            onChange={setHidden}
            saving={saving === "finish"}
            onBack={() => setStep(0)}
            onFinish={() => commit(hidden, "finish")}
          />
        )}
        {step === 2 && <ReadyStep hidden={hidden} onOpen={() => router.replace("/")} />}
      </div>
    </Page>
  )
}

/**
 * What the server is for, as cards you pick any number of. Each says which
 * sections it keeps, drawn as the rail draws them, so the reader is choosing
 * a sidebar and can see it.
 *
 * On a server that already has projects or databases — an account made for a
 * second person on a working machine — those purposes start chosen, and the
 * head says why. A fresh install has neither and starts with nothing chosen.
 */
function PurposeStep({
  picked,
  onPick,
  skipping,
  onSkip,
  onContinue,
}: {
  picked: PurposeKey[] | null
  onPick: (purposes: PurposeKey[]) => void
  skipping: boolean
  onSkip: () => void
  onContinue: (purposes: PurposeKey[]) => void
}) {
  const fleet = usePoll((signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal), 0)
  const databases = usePoll((signal) => get<DbConnection[]>("/databases/", undefined, signal), 0)
  const projects = fleet.data?.deployments.length ?? 0
  const connections = databases.data?.length ?? 0
  const found: PurposeKey[] = [
    ...(projects > 0 ? (["ship"] as const) : []),
    ...(connections > 0 ? (["databases"] as const) : []),
  ]
  // Untouched, the choice is whatever the server suggests; the first press
  // makes it the reader's, and a read landing after that changes nothing.
  const chosen = picked ?? found

  const toggle = (key: PurposeKey) =>
    onPick(chosen.includes(key) ? chosen.filter((k) => k !== key) : [...chosen, key])

  const readings = [
    projects > 0 && plural(projects, "project"),
    connections > 0 && plural(connections, "database"),
  ].filter(Boolean)

  return (
    <FlowPanel className="xl:max-h-full xl:min-h-0">
      <FlowPanelHeader
        title="Choose everything that applies"
        actions={
          readings.length > 0 && (
            <span className="numeric text-hint text-muted-foreground">
              {readings.join(" and ")} already on this server
            </span>
          )
        }
      />
      <FlowPanelBody className="min-h-0 overflow-y-auto">
        <ChoiceGrid role="group" aria-label="Purposes">
          {PURPOSES.map((purpose, index) => (
            <ChoiceCard
              key={purpose.key}
              index={index}
              verb={purpose.title}
              title={purpose.title}
              description={purpose.hint}
              mark={PURPOSE_MARKS[purpose.key]}
              selected={chosen.includes(purpose.key)}
              onClick={() => toggle(purpose.key)}
              trailing={<SectionsLine sections={purpose.sections} />}
            />
          ))}
        </ChoiceGrid>
      </FlowPanelBody>
      <FlowActions
        note="This decides what your sidebar shows, not what you can do. Every page stays one search away."
        secondary={
          <Button variant="ghost" onClick={onSkip} pending={skipping}>
            Show me everything
          </Button>
        }
      >
        <Button disabled={chosen.length === 0 || skipping} onClick={() => onContinue(chosen)}>
          Continue
        </Button>
      </FlowActions>
    </FlowPanel>
  )
}

/** The sections a purpose keeps, each with the glyph its row has in the rail. */
function SectionsLine({ sections }: { sections: readonly SectionKey[] }) {
  return (
    <span className="mt-auto flex flex-wrap gap-x-3 gap-y-1 pt-1.5 text-hint text-muted-foreground">
      {sections.map((key) => {
        const entry = sectionEntry(key)
        if (!entry) return null
        return (
          <span key={key} className="inline-flex items-center gap-1">
            <entry.icon aria-hidden className="size-3 shrink-0" />
            {entry.title}
          </span>
        )
      })}
    </span>
  )
}

/**
 * The purposes' answer as switches, beside the rail it will draw. This is the
 * screen that decides; the first one only filled it in.
 */
function SidebarStep({
  hidden,
  onChange,
  saving,
  onBack,
  onFinish,
}: {
  hidden: Set<string>
  onChange: (hidden: Set<string>) => void
  saving: boolean
  onBack: () => void
  onFinish: () => void
}) {
  const { shown, total } = useSectionCount(hidden)
  return (
    <div className="grid min-w-0 gap-x-6 gap-y-6 xl:h-full xl:min-h-0 xl:grid-cols-[minmax(0,1fr)_22rem] xl:grid-rows-[minmax(0,1fr)]">
      <FlowPanel className="min-w-0 xl:max-h-full xl:min-h-0 xl:self-start">
        <FlowPanelHeader
          title="Sections"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {shown} of {total} shown
            </span>
          }
        />
        <FlowPanelBody className="min-h-0 overflow-y-auto xl:flex-1">
          <SectionSwitches hidden={hidden} onChange={onChange} />
        </FlowPanelBody>
        <FlowActions
          note="A hidden section still opens from any link to it, and search finds every page."
          secondary={
            <Button variant="ghost" onClick={onBack} disabled={saving}>
              Back
            </Button>
          }
        >
          <Button onClick={onFinish} pending={saving}>
            Finish
          </Button>
        </FlowActions>
      </FlowPanel>
      <div className="min-w-0 space-y-2 xl:min-h-0 xl:overflow-y-auto">
        <p className="eyebrow px-1">Your sidebar</p>
        <SidebarPreview hidden={hidden} />
      </div>
    </div>
  )
}

/** The outcome, said once, with what hiding a section does and does not do. */
function ReadyStep({ hidden, onOpen }: { hidden: ReadonlySet<string>; onOpen: () => void }) {
  const { shown, total } = useSectionCount(hidden)
  const keys = useLauncherKeys()
  const afterwards: { icon: Icon; title: string; text: string }[] = [
    {
      icon: Link,
      title: "Links still lead where they did",
      text: "A deployment's domain still opens its site in the proxy. The sidebar shows a hidden section while you are in it, with a way to keep it.",
    },
    {
      icon: MagnifyingGlass,
      title: "Search reaches every page",
      text: `${keys} finds hidden pages as soon as you type, with every container, site and database.`,
    },
    {
      icon: SettingsSliders,
      title: "Change it whenever you like",
      text: "Account → Workspace has the same switches, and can ask you this again.",
    },
  ]

  return (
    <div className="grid min-w-0 gap-x-6 gap-y-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
      <FlowPanel className="min-w-0 xl:self-start">
        <FlowPanelHeader
          title={`${shown} of ${total} sections in your sidebar`}
          actions={
            total > shown && (
              <span className="numeric text-hint text-muted-foreground">
                {total - shown} hidden
              </span>
            )
          }
        />
        <FlowPanelBody>
          <ul className="grid gap-4">
            {afterwards.map((item) => (
              <li key={item.title} className="flex min-w-0 items-start gap-3">
                <item.icon aria-hidden className="mt-0.5 size-4 shrink-0 text-brand" />
                <div className="min-w-0 space-y-0.5">
                  <p className="text-body font-medium">{item.title}</p>
                  <p className="text-hint leading-relaxed text-muted-foreground">{item.text}</p>
                </div>
              </li>
            ))}
          </ul>
        </FlowPanelBody>
        <FlowActions>
          <Button onClick={onOpen}>Open the dashboard</Button>
        </FlowActions>
      </FlowPanel>
      <div className="min-w-0 space-y-2">
        <p className="eyebrow px-1">Your sidebar</p>
        <SidebarPreview hidden={hidden} />
      </div>
    </div>
  )
}
