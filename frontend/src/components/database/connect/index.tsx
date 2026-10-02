"use client"

import { useState } from "react"
import Link from "next/link"
import { ArrowLeft, Linked, MagnifyingGlass, Play, type Icon } from "@/components/icons"
import { FlowHeader, FlowSteps } from "@/components/flow"
import { Page } from "@/components/page"
import { ProductLogos } from "@/components/product-logo"
import { EmptyState } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { ConnectExisting } from "@/components/database/connect/connect-form"
import { FoundHere } from "@/components/database/connect/found"
import { StartNew } from "@/components/database/connect/start"
import { useAddress } from "@/components/database/fleet/use-address"
import { useDatabases } from "@/components/database/shell/databases-context"
import { DATABASES_HREF, type AddMode } from "@/components/database/shell/routes"

const MODES: { key: AddMode; label: string; icon: Icon }[] = [
  { key: "start", label: "Start a new one here", icon: Play },
  { key: "connect", label: "Connect to one", icon: Linked },
  { key: "found", label: "Found on this server", icon: MagnifyingGlass },
]

/** The sequence each way in is, as its own steps (§17 pass 3). */
const STEPS: Record<AddMode, { key: string; label: string }[]> = {
  start: [
    { key: "engine", label: "Engine" },
    { key: "settings", label: "Settings" },
    { key: "create", label: "Create" },
  ],
  connect: [
    { key: "engine", label: "Engine" },
    { key: "address", label: "Address" },
    { key: "connect", label: "Connect" },
  ],
  found: [
    { key: "pick", label: "Pick one" },
    { key: "connect", label: "Connect" },
  ],
}

const Eyebrow = (
  <Link
    href={DATABASES_HREF}
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Databases
  </Link>
)

/**
 * Add a database: start one in a container, connect one that already runs
 * somewhere, or take one found on this machine. A sequence with an outcome —
 * a database's home — so the page is drawn in the flow register (§16).
 *
 * The question is the page's own rank and the three answers are a strip of
 * pressed buttons under it, not tabs: three toggles for one answer. Which
 * answer is open, and the engine or the found server it is open on, are in
 * the address (`?mode=`, `?engine=`, `?key=`), so each is a link somebody can
 * be sent and Back steps out of it. The whole sequence is held to the window
 * at the width that sets the catalogue beside its settings; what scrolls is
 * the one list or form longer than the space left.
 */
export function AddDatabase() {
  const { admin } = useDatabases()
  const address = useAddress()
  const asked = address.read("mode")
  const mode: AddMode = asked === "connect" || asked === "found" ? asked : "start"
  const engine = address.read("engine")
  // The step past the choice is the screen's own doing — a request in
  // flight — so it is told here rather than read from the address.
  const [working, setWorking] = useState(false)
  const step = mode === "found" ? (working ? 1 : 0) : working ? 2 : engine ? 1 : 0
  const onStep = (next: number) => setWorking(next === 2)

  if (!admin) {
    return (
      <Page register="flow" className="animate-rise">
        <FlowHeader eyebrow={Eyebrow} question="Where is the database?" />
        <EmptyState
          mark={<ProductLogos ids={["postgres", "mysql", "redis"]} size="md" />}
          title="Adding a database needs an administrator"
          description="Starting a server, saving a connection and reading what runs on this machine are an administrator's to do."
          action={
            <Button size="sm" variant="outline" asChild>
              <Link href={DATABASES_HREF}>Back to the databases</Link>
            </Button>
          }
        />
      </Page>
    )
  }

  return (
    <Page register="flow" fill="xl" className="animate-rise">
      <FlowHeader
        eyebrow={Eyebrow}
        question="Where is the database?"
        steps={<FlowSteps steps={STEPS[mode]} current={step} />}
      />
      {/* A group of pressed buttons, not a tablist: a tablist must own tabs,
          and these are three toggles for one answer. On a phone the strip
          runs to the screen's edge and scrolls there. */}
      <div
        role="group"
        aria-label="Where the database is"
        className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline max-sm:-mx-5 max-sm:px-5 [&::-webkit-scrollbar]:hidden"
      >
        {MODES.map((option) => (
          <button
            key={option.key}
            type="button"
            aria-pressed={mode === option.key}
            onClick={() => {
              setWorking(false)
              address.set({ mode: option.key, engine: null, key: null })
            }}
            className={tabClasses(mode === option.key, "h-11 max-sm:px-2")}
          >
            <option.icon
              aria-hidden
              className={
                mode === option.key ? "size-3.5 text-brand" : "size-3.5 text-muted-foreground"
              }
            />
            {option.label}
          </button>
        ))}
      </div>
      <div className="min-w-0 xl:min-h-0 xl:flex-1">
        {mode === "start" && (
          <StartNew
            engine={engine}
            onChoose={(next) => address.set({ mode: "start", engine: next })}
            onStep={onStep}
          />
        )}
        {mode === "connect" && (
          <ConnectExisting
            key={engine}
            engine={engine}
            onChoose={(next) => address.set({ mode: "connect", engine: next })}
            onStep={onStep}
          />
        )}
        {mode === "found" && <FoundHere first={address.read("key")} onStep={onStep} />}
      </div>
    </Page>
  )
}
