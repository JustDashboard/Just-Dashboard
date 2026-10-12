"use client"

import { useEffect, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import { ArrowRight, Check, Logout } from "@/components/icons"
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
import { ChoiceCard } from "@/components/choice-card"
import { ProductLogo } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { displayNameOf } from "@/components/account/user-avatar"
import { Logo } from "@/components/logo"
import { ServerPicture } from "@/components/onboarding/server-picture"
import { useSectionCount } from "@/components/onboarding/section-tiles"
import { BlurFade } from "@/components/ui/blur-fade"
import { Button } from "@/components/ui/button"
import { Confetti, type ConfettiRef } from "@/components/ui/confetti"

/**
 * The first thing a new account sees after signing in: one question and its
 * answer, on two screens.
 *
 * The question is the purposes as big cards and nothing beside them. Continue
 * saves the sidebar those purposes keep and opens the answer: this server
 * wired to each purpose, filling the screen, with one command under it. The
 * sections themselves are not asked about here — they were a second screen of
 * fifteen tiles beside a preview of the rail, which made a first sign-in three
 * steps long — and are Account → Workspace's to change afterwards.
 *
 * A flow page (§16) on arrival's ground, the one sign-in stands on. Nothing is
 * saved until Continue or Show me everything, and either one ends the
 * question for good.
 */
export function WelcomeFlow() {
  const router = useRouter()
  const { status, logout } = useAuth()
  const { save } = useHiddenSections()
  const [answered, setAnswered] = useState(false)
  // Null until the reader presses a purpose: until then the choice is what
  // the server already runs.
  const [picked, setPicked] = useState<PurposeKey[] | null>(null)
  const [saving, setSaving] = useState<"continue" | "skip" | null>(null)
  const confetti = useRef<ConfettiRef>(null)
  const page = useRef<HTMLDivElement>(null)
  const name = status?.user ? displayNameOf(status.user) : ""
  const found = useFound()
  const purposes = picked ?? found.purposes
  const { shown } = useSectionCount(new Set(hiddenFor(purposes)))

  // The answer is a new screen. On a phone Continue is pressed at the foot of
  // six cards, and the picture would otherwise open scrolled to wherever that was.
  useEffect(() => {
    page.current?.parentElement?.scrollTo({ top: 0 })
  }, [answered])

  const commit = async (how: "continue" | "skip") => {
    setSaving(how)
    try {
      await save(how === "skip" ? [] : hiddenFor(purposes))
      if (how === "skip") {
        router.replace("/")
        return
      }
      setAnswered(true)
      // The outcome landing in front of the reader, once (§17 pass 7).
      confetti.current?.fire()
    } catch (err) {
      notify.error("Could not save your sidebar", err)
    } finally {
      setSaving(null)
    }
  }

  return (
    <Page ref={page} register="flow" className="min-h-full animate-rise px-4 md:px-8">
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

      {/* Keyed, so the answer rises in as the question leaves. */}
      <div
        key={answered ? "answer" : "question"}
        className="mx-auto flex w-full max-w-5xl min-w-0 flex-1 animate-rise flex-col justify-center gap-8 md:gap-10"
      >
        <header className="space-y-2 text-center">
          <p className="eyebrow">Welcome{name ? `, ${name}` : ""}</p>
          <h1 className="text-2xl leading-tight font-semibold tracking-tight text-balance">
            {answered ? "Your server is ready" : "What will you use this server for?"}
          </h1>
        </header>

        {answered ? (
          <>
            <ServerPicture purposes={purposes} />
            <div className="flex flex-col items-center gap-3">
              <Button size="lg" className="min-w-56" onClick={() => router.replace("/")}>
                Go to dashboard
                <ArrowRight className="size-4" />
              </Button>
              <p className="text-center text-hint text-muted-foreground">
                {plural(shown, "section")} in your sidebar. Change them any time in Account →
                Workspace.
              </p>
            </div>
          </>
        ) : (
          <>
            <Purposes chosen={purposes} readings={found.readings} onPick={setPicked} />
            {/* Under the cards on every screen. A phone held them in a dark
                bar stuck to the foot of the window, which the wide screen
                does not have and which read as a second surface; six cards as
                rows fit a phone with the command under them anyway. */}
            <div className="flex items-center justify-center gap-3">
              <Button
                variant="ghost"
                size="lg"
                onClick={() => commit("skip")}
                pending={saving === "skip"}
                disabled={saving === "continue"}
              >
                Show me everything
              </Button>
              <Button
                size="lg"
                className="min-w-40"
                disabled={purposes.length === 0 || saving === "skip"}
                pending={saving === "continue"}
                onClick={() => commit("continue")}
              >
                Continue
                {saving !== "continue" && <ArrowRight className="size-4" />}
              </Button>
            </div>
          </>
        )}
      </div>
    </Page>
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
 * What the server is for, as big cards you pick any number of, each drawn as
 * the products it means. A tile on a wide screen and a row on a phone, where
 * six tiles stacked were five screens of logos.
 */
function Purposes({
  chosen,
  readings,
  onPick,
}: {
  chosen: PurposeKey[]
  readings: Partial<Record<PurposeKey, string>>
  onPick: (purposes: PurposeKey[]) => void
}) {
  const toggle = (key: PurposeKey) =>
    onPick(chosen.includes(key) ? chosen.filter((k) => k !== key) : [...chosen, key])

  return (
    <div
      role="group"
      aria-label="Purposes"
      className="grid min-w-0 gap-2.5 sm:grid-cols-2 sm:gap-4 lg:grid-cols-3"
    >
      {PURPOSES.map((purpose, index) => {
        const on = chosen.includes(purpose.key)
        const reading = readings[purpose.key]
        return (
          <BlurFade key={purpose.key} delay={index * 0.04} className="h-full">
            <ChoiceCard
              selected={on}
              onClick={() => toggle(purpose.key)}
              className="min-h-18 flex-row items-center gap-3.5 px-4 py-3 sm:min-h-44 sm:flex-col sm:items-stretch sm:justify-between sm:gap-6 sm:p-6"
            >
              <span className="flex shrink-0 items-start justify-between gap-3">
                <span className="flex gap-2">
                  {purpose.products.map((product) => (
                    <ProductLogo key={product} id={product} className="max-sm:size-9" />
                  ))}
                </span>
                <Tick on={on} className="max-sm:hidden" />
              </span>
              <span className="grid min-w-0 flex-1 gap-1.5">
                <span className="text-base leading-tight font-semibold sm:text-lg">
                  {purpose.title}
                </span>
                {reading && <Status tone="running" label={reading} className="text-hint" />}
              </span>
              <Tick on={on} className="sm:hidden" />
            </ChoiceCard>
          </BlurFade>
        )
      })}
    </div>
  )
}

function Tick({ on, className }: { on: boolean; className?: string }) {
  return (
    <Check
      aria-hidden
      className={cn(
        "size-5 shrink-0 text-brand transition-opacity",
        on ? "opacity-100" : "opacity-0",
        className,
      )}
    />
  )
}
