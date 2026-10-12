"use client"

import { useEffect, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import {
  Check,
  Link,
  Logout,
  MagnifyingGlass,
  SettingsSliders,
  type Icon,
} from "@/components/icons"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { DbConnection, DeploymentFleet } from "@/lib/types"
import { PURPOSES, hiddenFor, type PurposeKey } from "@/lib/sidebar-sections"
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
import { ProductLogo } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { displayNameOf } from "@/components/account/user-avatar"
import { useLauncherKeys } from "@/components/command-search/keycaps"
import { Logo } from "@/components/logo"
import { ServerPicture } from "@/components/onboarding/server-picture"
import {
  SectionTiles,
  SidebarPreview,
  useSectionCount,
} from "@/components/onboarding/section-tiles"
import { BlurFade } from "@/components/ui/blur-fade"
import { Button } from "@/components/ui/button"
import { Confetti, type ConfettiRef } from "@/components/ui/confetti"
import { NumberTicker } from "@/components/ui/number-ticker"

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

// Two columns from `xl`: the thing being decided, and the picture it draws.
const COLUMNS =
  "grid min-w-0 gap-x-6 gap-y-6 xl:h-full xl:min-h-0 xl:grid-cols-[minmax(0,1fr)_22rem] xl:grid-rows-[minmax(0,1fr)]"

/**
 * The first thing a new account sees after signing in: what the server is
 * for, which sections that keeps in the sidebar, and the outcome.
 *
 * A flow page (§16): a sequence with an outcome, the account's sidebar. Each
 * screen is one surface being decided beside a picture of what it makes —
 * the server wired to its purposes, then the sidebar itself — so the answer
 * is seen rather than read. The words are the question, the names and the
 * counts; what a section is for is on its page.
 *
 * The purposes are a first draft, not the answer: the second screen is the
 * sections themselves, so a choice that brought Network to somebody who only
 * wanted the proxy is undone in a press. Nothing is saved until Finish or
 * Show me everything, and either one ends the question for good.
 */
export function WelcomeFlow() {
  const router = useRouter()
  const { status, logout } = useAuth()
  const { save } = useHiddenSections()
  const [step, setStep] = useState(0)
  // Null until the reader presses a purpose: until then the choice is what
  // the server already runs. Held here so Back returns to the cards as they were.
  const [picked, setPicked] = useState<PurposeKey[] | null>(null)
  const [hidden, setHidden] = useState<Set<string>>(new Set())
  const [saving, setSaving] = useState<"finish" | "skip" | null>(null)
  const confetti = useRef<ConfettiRef>(null)
  const page = useRef<HTMLDivElement>(null)
  const name = status?.user ? displayNameOf(status.user) : ""
  const found = useFound()
  const purposes = picked ?? found.purposes

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
          <div className={COLUMNS}>
            <PurposeStep
              chosen={purposes}
              readings={found.readings}
              onPick={setPicked}
              skipping={saving === "skip"}
              onSkip={() => commit(new Set(), "skip")}
              onContinue={() => {
                setHidden(new Set(hiddenFor(purposes)))
                setStep(1)
              }}
            />
            <Beside label="Your server">
              <ServerPicture purposes={purposes} />
            </Beside>
          </div>
        )}
        {step === 1 && (
          <div className={COLUMNS}>
            <SidebarStep
              hidden={hidden}
              onChange={setHidden}
              saving={saving === "finish"}
              onBack={() => setStep(0)}
              onFinish={() => commit(hidden, "finish")}
            />
            <Beside label="Your sidebar">
              <SidebarPreview hidden={hidden} />
            </Beside>
          </div>
        )}
        {step === 2 && (
          <div className={COLUMNS}>
            <ReadyStep purposes={purposes} hidden={hidden} onOpen={() => router.replace("/")} />
            <Beside label="Your sidebar">
              <SidebarPreview hidden={hidden} />
            </Beside>
          </div>
        )}
      </div>
    </Page>
  )
}

/** The column beside the focused surface: a picture of what it makes, named. */
function Beside({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0 space-y-2 xl:min-h-0 xl:overflow-y-auto">
      <p className="eyebrow px-1">{label}</p>
      {children}
    </div>
  )
}

/**
 * What the server already runs, read once: its deployment projects and saved
 * databases. An account made for a second person on a working machine starts
 * with those purposes chosen and the count on their cards; a fresh install
 * has neither and starts with nothing chosen.
 */
function useFound() {
  const fleet = usePoll((signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal), 0)
  const databases = usePoll((signal) => get<DbConnection[]>("/databases/", undefined, signal), 0)
  const projects = fleet.data?.deployments.length ?? 0
  const connections = databases.data?.length ?? 0
  const readings: Partial<Record<PurposeKey, string>> = {
    ...(projects > 0 && { ship: `${plural(projects, "project")} here` }),
    ...(connections > 0 && { databases: `${plural(connections, "database")} here` }),
  }
  return { purposes: Object.keys(readings) as PurposeKey[], readings }
}

/**
 * What the server is for, as cards you pick any number of, each drawn as the
 * products it means. Pressing one wires it to the server in the picture
 * beside.
 */
function PurposeStep({
  chosen,
  readings,
  onPick,
  skipping,
  onSkip,
  onContinue,
}: {
  chosen: PurposeKey[]
  readings: Partial<Record<PurposeKey, string>>
  onPick: (purposes: PurposeKey[]) => void
  skipping: boolean
  onSkip: () => void
  onContinue: () => void
}) {
  const toggle = (key: PurposeKey) =>
    onPick(chosen.includes(key) ? chosen.filter((k) => k !== key) : [...chosen, key])

  return (
    <FlowPanel className="min-w-0 xl:max-h-full xl:min-h-0 xl:self-start">
      <FlowPanelBody className="min-h-0 overflow-y-auto p-5 xl:flex-1">
        <ChoiceGrid role="group" aria-label="Purposes">
          {PURPOSES.map((purpose, index) => {
            const on = chosen.includes(purpose.key)
            const reading = readings[purpose.key]
            return (
              <BlurFade key={purpose.key} delay={index * 0.04} className="h-full">
                <ChoiceCard
                  selected={on}
                  onClick={() => toggle(purpose.key)}
                  className="min-h-32 justify-between gap-5 p-4"
                >
                  <span className="flex w-full items-start justify-between gap-3">
                    <span className="flex gap-1.5">
                      {purpose.products.map((product) => (
                        <ProductLogo key={product} id={product} size="sm" />
                      ))}
                    </span>
                    <Check
                      aria-hidden
                      className={cn(
                        "size-4 shrink-0 text-brand transition-opacity",
                        on ? "opacity-100" : "opacity-0",
                      )}
                    />
                  </span>
                  <span className="grid gap-1">
                    <span className="text-title leading-tight font-semibold">{purpose.title}</span>
                    {reading && <Status tone="running" label={reading} className="text-hint" />}
                  </span>
                </ChoiceCard>
              </BlurFade>
            )
          })}
        </ChoiceGrid>
      </FlowPanelBody>
      <FlowActions
        secondary={
          <Button variant="ghost" onClick={onSkip} pending={skipping}>
            Show me everything
          </Button>
        }
      >
        <Button disabled={chosen.length === 0 || skipping} onClick={onContinue}>
          Continue
        </Button>
      </FlowActions>
    </FlowPanel>
  )
}

/** The purposes' answer as the sections themselves: the screen that decides. */
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
    <FlowPanel className="min-w-0 xl:max-h-full xl:min-h-0 xl:self-start">
      <FlowPanelHeader
        title="Sections"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {shown} of {total} shown
          </span>
        }
      />
      <FlowPanelBody className="min-h-0 overflow-y-auto p-5 xl:flex-1">
        <SectionTiles hidden={hidden} onChange={onChange} />
      </FlowPanelBody>
      <FlowActions
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
  )
}

/** The outcome: the server wired to its purposes, the count, and three facts. */
function ReadyStep({
  purposes,
  hidden,
  onOpen,
}: {
  purposes: PurposeKey[]
  hidden: ReadonlySet<string>
  onOpen: () => void
}) {
  const { shown, total } = useSectionCount(hidden)
  const keys = useLauncherKeys()
  const facts: { icon: Icon; text: string }[] = [
    { icon: Link, text: "Links still open hidden sections" },
    { icon: MagnifyingGlass, text: `${keys} finds every page` },
    { icon: SettingsSliders, text: "Change it in Account → Workspace" },
  ]

  return (
    <FlowPanel className="min-w-0 xl:self-start">
      <FlowPanelHeader
        title={
          <span>
            <NumberTicker value={shown} className="numeric" /> of {total} sections in your sidebar
          </span>
        }
      />
      <FlowPanelBody className="p-0">
        <ServerPicture purposes={purposes} className="py-8" />
        <ul className="grid gap-3 border-t border-hairline px-5 py-4 sm:grid-cols-3">
          {facts.map((fact) => (
            <li key={fact.text} className="flex min-w-0 items-center gap-2 text-hint">
              <fact.icon aria-hidden className="size-4 shrink-0 text-brand" />
              <span className="min-w-0">{fact.text}</span>
            </li>
          ))}
        </ul>
      </FlowPanelBody>
      <FlowActions>
        <Button onClick={onOpen}>Open the dashboard</Button>
      </FlowActions>
    </FlowPanel>
  )
}
