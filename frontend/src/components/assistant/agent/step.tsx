"use client"

import { createContext, useContext, useState } from "react"
import Link from "next/link"
import { AnimatePresence, motion } from "motion/react"
import {
  ArrowRight,
  Check,
  ChevronRight,
  Cross,
  LoaderCircle,
  LockClosed,
  TerminalWindow,
  Warning,
} from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { cn } from "@/lib/utils"
import { Well } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { MethodWord, RequestPath } from "@/components/deploy/request-marks"
import { useConversationRelease } from "@/components/assistant/elements/conversation"
import { stepMark } from "@/components/assistant/agent/marks"
import { arriveAt } from "@/components/assistant/agent/arrival"
import {
  inputLines,
  type AgentCall,
  type AgentChange,
  type AgentLink,
  type AgentStep,
} from "@/components/assistant/agent/model"

const EASE = [0.16, 1, 0.3, 1] as const

/**
 * A route the assistant called as the person: the method as the request log draws
 * it — a read muted, a write in the method hue — on a recessed chip, and the path
 * beside it in the path hue. `GET /databases/connections` reads as a request before
 * it reads as words.
 */
export function ApiRoute({ route, className }: { route: string; className?: string }) {
  const space = route.indexOf(" ")
  const method = space > 0 ? route.slice(0, space).toUpperCase() : ""
  const path = space > 0 ? route.slice(space + 1) : route
  return (
    <span
      data-slot="agent-route"
      className={cn("inline-flex max-w-full min-w-0 items-baseline gap-1.5 text-hint", className)}
    >
      {method && (
        <MethodWord
          method={method}
          plain={method === "GET"}
          className="shrink-0 rounded-sm bg-surface-sunken px-1 py-px text-micro font-semibold"
        />
      )}
      <RequestPath path={path} className="text-hint" />
    </span>
  )
}

/**
 * What a chat's terminal can be asked from its step: the newest call that left the
 * window in a state worth saying, and how the person types into it. The chat that
 * draws the steps provides it; a step drawn anywhere else says nothing of its window.
 */
export const AgentTerminalContext = createContext<{
  latest?: string
  type: (text: string) => Promise<void>
} | null>(null)

/** Whether a call is a route the assistant called itself, drawn with its request. */
const routed = (call: AgentCall) => call.tool === "api_request" && Boolean(call.route)

/** Finding which routes to call: said in a word, as something it looked up. */
const lookup = (call: AgentCall) => call.kind === "free"

/**
 * The shell command a call runs on the host, when it runs one: drawn as itself in
 * mono, because a person reads the command, not a sentence about it, to decide.
 */
export function commandOf(call: AgentCall): string | undefined {
  if (call.tool !== "run_command" && call.tool !== "terminal_run") return
  if (!call.input || typeof call.input !== "object") return
  const command = (call.input as Record<string, unknown>).command
  return typeof command === "string" && command.trim() ? command : undefined
}

/** A command as it will run, whole and wrapped, with the prompt's `$` before it. */
function CommandLine({ command, className }: { command: string; className?: string }) {
  return (
    <pre
      data-slot="agent-command"
      className={cn(
        "max-h-40 overflow-auto rounded-md bg-surface-sunken px-2.5 py-1.5 font-mono text-hint leading-relaxed break-all whitespace-pre-wrap",
        className,
      )}
    >
      <span aria-hidden className="text-muted-foreground select-none">
        ${" "}
      </span>
      {command}
    </pre>
  )
}

/**
 * What a call was asked, less what its row already says: a route's method and path
 * are drawn as the route, and its reason as a sentence, so only its query and body
 * are left to list.
 */
function askedInput(call: AgentCall): unknown {
  if (!call.input || typeof call.input !== "object") return call.input
  const rest: Record<string, unknown> = { ...(call.input as Record<string, unknown>) }
  if (call.tool === "api_request") {
    delete rest.method
    delete rest.path
    delete rest.reason
  } else if (commandOf(call)) {
    delete rest.command
    delete rest.reason
  } else return call.input
  return rest
}

/**
 * One thing the assistant did, or is doing. AI Elements' `tool` and
 * `chain-of-thought`, from its registry (vercel/ai-elements, Apache-2.0), in this
 * product's marks: the product it touched on a tile, and a line of what it came to
 * that lights while it runs. Its badge turns from the working spinner to a tick or
 * a cross as it ends. Unfolding one shows what it was asked and what it read, and
 * the fold opens to its height rather than appearing.
 */
export function StepRow({ step, replayed }: { step: AgentStep; replayed?: boolean }) {
  const [open, setOpen] = useState(false)
  const release = useConversationRelease()
  const mark = stepMark(step.call)
  const Glyph = mark.icon
  const command = commandOf(step.call)
  const details =
    Boolean(command) ||
    inputLines(askedInput(step.call)).lines.length > 0 ||
    Boolean(step.output) ||
    // What went wrong, past what its row has room for.
    (step.state === "failed" && (step.said?.length ?? 0) > 60) ||
    Boolean(step.call.reason && !step.change) ||
    routed(step.call)
  const running = step.state === "running"
  const quiet = lookup(step.call)
  // The newest call into the chat's terminal, left asking the person or the model
  // something, is waiting rather than done, whatever the call itself came to.
  const terminal = useContext(AgentTerminalContext)
  const waitingOn =
    step.state === "done" &&
    terminal?.latest === step.call.id &&
    (step.terminal?.state === "password" || step.terminal?.state === "input")
  return (
    <Collapsible
      open={open}
      onOpenChange={(next) => {
        if (next) release()
        setOpen(next)
      }}
      asChild
    >
      <li
        data-slot="agent-step"
        data-state={step.state}
        data-kind={step.call.kind}
        data-tool={step.call.tool}
        className="animate-rise"
      >
        <CollapsibleTrigger
          disabled={!details}
          className={cn(
            "group flex min-h-9 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 py-1.5 text-left focus-ring",
            details && "transition-colors hover:bg-row-hover",
          )}
        >
          <span
            aria-hidden
            className={cn(
              "relative grid size-6 shrink-0 place-items-center rounded-md border transition-colors duration-300",
              quiet ? "border-transparent" : "border-hairline bg-surface-header",
              running && !quiet && "border-signal/50",
            )}
          >
            {mark.product ? (
              <ProductGlyph id={mark.product} className="size-3.5" />
            ) : (
              <Glyph className="size-3.5 text-muted-foreground" />
            )}
            {!quiet && <StepBadge state={waitingOn ? "waiting" : step.state} />}
          </span>
          <span className="min-w-0 flex-1 truncate text-body">
            {running ? (
              <TextShimmer
                className={cn("max-w-full truncate align-bottom", command && "font-mono text-hint")}
              >
                {command ?? step.call.label}
              </TextShimmer>
            ) : (
              <span
                className={cn(
                  step.state === "declined" && "text-muted-foreground line-through",
                  quiet && "text-muted-foreground",
                  command && "font-mono text-hint",
                )}
              >
                {command ?? step.call.label}
              </span>
            )}
            {routed(step.call) && (
              <ApiRoute route={step.call.route!} className="ml-2 align-baseline max-sm:hidden" />
            )}
            {step.said && step.state !== "running" && (
              <span
                className={cn(
                  "ml-2 text-hint",
                  // A call the assistant wrote wrong and corrected is its own slip,
                  // not something that failed on the server.
                  step.state === "failed" && !quiet ? "text-destructive" : "text-muted-foreground",
                )}
              >
                {step.said}
              </span>
            )}
            {step.state === "declined" && (
              <span className="ml-2 text-hint text-muted-foreground no-underline">Declined</span>
            )}
          </span>
          {details && (
            <ChevronRight
              aria-hidden
              className="size-3.5 shrink-0 text-muted-foreground transition-transform duration-200 group-data-[state=open]:rotate-90"
            />
          )}
        </CollapsibleTrigger>
        {details && (
          <CollapsibleContent className="overflow-hidden data-[state=closed]:animate-collapsible-up data-[state=open]:animate-collapsible-down">
            <div className="space-y-2 px-2 pt-1 pb-2 sm:pl-10.5">
              <StepDetails step={step} />
            </div>
          </CollapsibleContent>
        )}
        {step.change && (
          <ChangeCard change={step.change} reason={step.call.reason} lit={!replayed} />
        )}
        {step.terminal && <TerminalNote step={step} />}
      </li>
    </Collapsible>
  )
}

/** The corner of a step's tile: a spinner while it runs, a tick or a cross once it ends. */
function StepBadge({ state }: { state: AgentStep["state"] }) {
  return (
    <span className="absolute -right-1 -bottom-1 grid size-3 place-items-center">
      <AnimatePresence mode="popLayout" initial={false}>
        {state === "done" ? (
          <Badge key="done" className="bg-success text-success-foreground">
            <Check className="size-2" />
          </Badge>
        ) : state === "failed" ? (
          <Badge key="failed" className="bg-destructive text-white">
            <Cross className="size-2" />
          </Badge>
        ) : state === "running" ? (
          <Badge key="running" className="bg-card text-signal">
            <LoaderCircle className="size-3 animate-spin" />
          </Badge>
        ) : state === "waiting" ? (
          <Badge key="waiting" className="size-2 animate-breathe bg-warning" />
        ) : null}
      </AnimatePresence>
    </span>
  )
}

function Badge({ className, children }: { className?: string; children?: React.ReactNode }) {
  return (
    <motion.span
      className={cn("grid size-3 place-items-center rounded-full", className)}
      initial={{ opacity: 0, scale: 0.4 }}
      animate={{ opacity: 1, scale: 1 }}
      exit={{ opacity: 0, scale: 0.4 }}
      transition={{ duration: 0.18, ease: EASE }}
    >
      {children}
    </motion.span>
  )
}

function StepDetails({ step }: { step: AgentStep }) {
  const command = commandOf(step.call)
  return (
    <>
      {command && <CommandLine command={command} />}
      {routed(step.call) ? (
        <ApiRoute route={step.call.route!} />
      ) : (
        step.call.operation && (
          <p className="truncate font-mono text-hint text-muted-foreground">
            {step.call.operation}
          </p>
        )
      )}
      {step.call.reason && !step.change && (
        <p className="text-hint leading-relaxed text-muted-foreground">{step.call.reason}</p>
      )}
      {step.state === "failed" && step.said && step.said.length > 60 && (
        <p className="text-hint leading-relaxed text-muted-foreground">{step.said}</p>
      )}
      <InputLines input={askedInput(step.call)} flush />
      {step.terminal && <Shortcut label="Terminal" href={terminalHref(step.terminal.session)} />}
      {step.output && (
        <Well
          className="max-h-56 p-2.5 text-hint whitespace-pre-wrap"
          aria-label={command ? "What it printed" : "What it read"}
        >
          {step.output}
        </Well>
      )}
    </>
  )
}

/** What a change did, the shortcut straight to it, and what is different now. */
function ChangeCard({
  change,
  reason,
  lit,
}: {
  change: AgentChange
  reason?: string
  /** Arrived just now, rather than read back: its shortcut is lit once. */
  lit?: boolean
}) {
  return (
    <div
      data-slot="agent-change"
      className="mx-1 mt-1 mb-1.5 animate-rise space-y-1.5 rounded-lg border border-rule-success bg-wash-success px-2.5 py-2 sm:mr-0 sm:ml-10.5"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1.5">
        <Check aria-hidden className="size-3.5 shrink-0 text-success" />
        <span className="min-w-0 flex-1 basis-40 text-hint leading-snug">
          <span className="font-medium text-success">Changed</span>
          {reason && <span className="text-muted-foreground"> · {reason}</span>}
        </span>
        <Shortcut label={change.label} href={change.href} lit={lit} />
      </div>
      {change.diff && change.diff.length > 0 && (
        <ul className="space-y-0.5 pl-5.5 font-mono text-hint">
          {change.diff.map((line) => (
            <li key={line.path} className="flex min-w-0 flex-wrap items-baseline gap-x-2">
              <span className="text-muted-foreground">{line.path}</span>
              {line.from && (
                <span className="break-all text-destructive line-through">{line.from}</span>
              )}
              {line.to && <span className="break-all text-success">{line.to}</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/**
 * A press straight to the page, the back way being the dock's. `lit` is a shortcut
 * that has just arrived: its outline is lit in the change's green once and fades,
 * so the way to what changed is the first thing the eye finds.
 */
export function Shortcut({ label, href, lit }: { label: string; href: string; lit?: boolean }) {
  return (
    <Button
      asChild
      size="xs"
      variant="outline"
      data-slot="agent-shortcut"
      className={cn(lit && "agent-attention [--agent-attention:var(--success)]")}
    >
      <Link href={href} onClick={() => arriveAt(href)}>
        Open {label}
        <ArrowRight />
      </Link>
    </Button>
  )
}

/** A shortcut the assistant offers, for what only the person can do. */
export function LinkCard({ link }: { link: AgentLink }) {
  return (
    <div
      data-slot="agent-link"
      className="flex min-w-0 animate-rise flex-wrap items-center gap-x-3 gap-y-1.5 rounded-lg border border-hairline bg-card p-2.5"
    >
      <p className="min-w-0 flex-1 basis-48 text-body text-muted-foreground">{link.note}</p>
      <Shortcut label={link.label} href={link.href} />
    </div>
  )
}

/** What a call would send, a line a value, so it is read rather than parsed. */
function InputLines({ input, flush }: { input: unknown; flush?: boolean }) {
  const { lines, more } = inputLines(input)
  if (lines.length === 0) return null
  return (
    <ul
      data-slot="agent-input"
      className={cn("space-y-0.5 font-mono text-hint", !flush && "pl-8.5")}
    >
      {lines.map((line) => (
        <li key={line.path} className="flex min-w-0 flex-wrap items-baseline gap-x-2">
          <span className="text-muted-foreground">{line.path}</span>
          <span className="min-w-0 break-all">{line.value}</span>
        </li>
      ))}
      {more > 0 && <li className="text-muted-foreground">and {more} more</li>}
    </ul>
  )
}

const KIND_TAG: Record<
  AgentStep["call"]["kind"],
  { label: string; tone: "default" | "warning" | "danger" }
> = {
  free: { label: "Reads nothing", tone: "default" },
  read: { label: "Reads", tone: "default" },
  change: { label: "Changes", tone: "warning" },
  destructive: { label: "Stops or replaces", tone: "danger" },
  server: { label: "The whole server", tone: "warning" },
}

/**
 * What it wants to do, waiting on the person's answer: AI Elements' `confirmation`.
 * Nothing it names has run. Each has its own Allow, so a person can take the read
 * and leave the restart; the press runs it through the route the project's own
 * button calls, as them. It arrives with a rise and its edge lit once in its tone,
 * because it is the one thing in the column that waits on the reader.
 */
export function ApprovalCard({
  steps,
  active,
  paused,
  onDecide,
}: {
  steps: AgentStep[]
  active: boolean
  paused?: boolean
  onDecide: (id: string | "all", allow: boolean) => void
}) {
  const open = steps.filter((step) => step.decision === undefined)
  const destructive = steps.some((step) => step.call.kind === "destructive")
  return (
    <motion.section
      data-slot="agent-approval"
      aria-label={
        steps.length === 1 ? "A call waiting for your answer" : "Calls waiting for your answer"
      }
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.22, ease: EASE }}
      className={cn(
        "space-y-2.5 rounded-xl border p-3",
        destructive ? "border-rule-danger bg-wash-danger" : "border-rule-warning bg-wash-warning",
        active &&
          (destructive
            ? "agent-attention [--agent-attention:var(--destructive)]"
            : "agent-attention [--agent-attention:var(--warning)]"),
      )}
    >
      <p className="flex items-center gap-2 text-body font-medium">
        <Warning
          aria-hidden
          className={cn("size-4", destructive ? "text-destructive" : "text-warning")}
        />
        {paused
          ? "It has taken many steps since you last asked. Carry on?"
          : steps.length === 1
            ? "It wants to do this"
            : `It wants to do ${steps.length} things`}
      </p>
      <ul className="divide-y divide-hairline">
        {steps.map((step) => {
          const tag = KIND_TAG[step.call.kind]
          const mark = stepMark(step.call)
          const Glyph = mark.icon
          return (
            <li key={step.call.id} className="space-y-1.5 py-2 first:pt-0 last:pb-0">
              <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1.5">
                <span
                  aria-hidden
                  className="grid size-6 shrink-0 place-items-center rounded-md border border-hairline bg-card"
                >
                  {mark.product ? (
                    <ProductGlyph id={mark.product} className="size-3.5" />
                  ) : (
                    <Glyph className="size-3.5 text-muted-foreground" />
                  )}
                </span>
                <span className="min-w-0 flex-1 text-body font-medium">{step.call.label}</span>
                <Tag tone={tag.tone}>{tag.label}</Tag>
                <AnimatePresence mode="popLayout" initial={false}>
                  {step.decision !== undefined ? (
                    <motion.span
                      key="decided"
                      initial={{ opacity: 0 }}
                      animate={{ opacity: 1 }}
                      transition={{ duration: 0.15 }}
                      className="text-hint font-medium text-muted-foreground"
                    >
                      {step.decision ? "Allowed" : "Declined"}
                    </motion.span>
                  ) : (
                    active && (
                      <motion.span
                        key="decide"
                        exit={{ opacity: 0 }}
                        transition={{ duration: 0.12 }}
                        className="flex shrink-0 gap-1.5"
                      >
                        <Button
                          size="xs"
                          variant={step.call.kind === "destructive" ? "destructive" : "default"}
                          aria-label={`Allow: ${step.call.label}`}
                          onClick={() => onDecide(step.call.id, true)}
                        >
                          Allow
                        </Button>
                        <Button
                          size="xs"
                          variant="outline"
                          aria-label={`Decline: ${step.call.label}`}
                          onClick={() => onDecide(step.call.id, false)}
                        >
                          Decline
                        </Button>
                      </motion.span>
                    )
                  )}
                </AnimatePresence>
              </div>
              {routed(step.call) && <ApiRoute route={step.call.route!} className="pl-8.5" />}
              {commandOf(step.call) && (
                <div className="pl-8.5">
                  <CommandLine command={commandOf(step.call)!} className="bg-card" />
                </div>
              )}
              {step.call.reason && (
                <p className="pl-8.5 text-body leading-relaxed text-muted-foreground">
                  {step.call.reason}
                </p>
              )}
              <InputLines input={askedInput(step.call)} />
            </li>
          )
        })}
      </ul>
      {active && open.length > 1 && (
        <div className="flex flex-wrap gap-2 border-t border-hairline pt-2.5">
          <Button size="sm" onClick={() => onDecide("all", true)}>
            Allow all
          </Button>
          <Button size="sm" variant="outline" onClick={() => onDecide("all", false)}>
            Decline all
          </Button>
        </div>
      )}
    </motion.section>
  )
}

const terminalHref = (session: string) => `/terminal?session=${encodeURIComponent(session)}`

/**
 * Under the newest call into the chat's terminal, what its window is doing when
 * that is worth a line: still running there, asking something, or asking for a
 * password — which is the person's to type, in a field that sends it to the window
 * and nowhere else.
 */
function TerminalNote({ step }: { step: AgentStep }) {
  const terminal = useContext(AgentTerminalContext)
  const held = step.terminal!
  if (!terminal || terminal.latest !== step.call.id) return null
  if (held.state === "password") return <PasswordField step={step} />
  if (held.state !== "running" && held.state !== "input") return null
  return (
    <div
      data-slot="agent-terminal"
      data-state={held.state}
      className="mx-1 mt-1 mb-1.5 flex min-w-0 animate-rise flex-wrap items-center gap-x-2 gap-y-1.5 rounded-lg border border-hairline bg-surface-header px-2.5 py-2 sm:mr-0 sm:ml-10.5"
    >
      <TerminalWindow aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
      <span className="min-w-0 flex-1 basis-40 text-hint leading-snug">
        {held.state === "running" ? (
          <span className="text-muted-foreground">Still running in its terminal</span>
        ) : (
          <>
            <span className="text-muted-foreground">Asking </span>
            <span className="font-mono">{held.prompt}</span>
          </>
        )}
      </span>
      <Shortcut label="Terminal" href={terminalHref(held.session)} />
    </div>
  )
}

/**
 * The password a command in the chat's terminal is waiting for. It is typed here
 * and sent to the window alone: never to the assistant, never into the chat.
 */
function PasswordField({ step }: { step: AgentStep }) {
  const terminal = useContext(AgentTerminalContext)!
  const held = step.terminal!
  const [value, setValue] = useState("")
  const [state, setState] = useState<"idle" | "sending" | "sent">("idle")
  const [error, setError] = useState<string>()
  const send = async () => {
    if (!value || state === "sending") return
    setState("sending")
    setError(undefined)
    try {
      await terminal.type(value)
      setValue("")
      setState("sent")
    } catch (err) {
      setState("idle")
      setError(errorMessage(err))
    }
  }
  return (
    <div
      data-slot="agent-terminal"
      data-state="password"
      className="mx-1 mt-1 mb-1.5 animate-rise space-y-2 rounded-lg border border-rule-warning bg-wash-warning px-2.5 py-2 sm:mr-0 sm:ml-10.5"
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1.5">
        <LockClosed aria-hidden className="size-3.5 shrink-0 text-warning" />
        <span className="min-w-0 flex-1 basis-40 text-hint leading-snug">
          <span className="font-medium text-warning">
            {state === "sent" ? "Sent to the terminal" : "Waiting for your password"}
          </span>
          {held.prompt && state !== "sent" && (
            <span className="font-mono text-muted-foreground"> · {held.prompt}</span>
          )}
        </span>
        <Shortcut label="Terminal" href={terminalHref(held.session)} />
      </div>
      {state !== "sent" && (
        <form
          className="flex min-w-0 gap-2"
          onSubmit={(event) => {
            event.preventDefault()
            void send()
          }}
        >
          <Input
            type="password"
            autoComplete="off"
            aria-label="Password for the terminal"
            placeholder="Password"
            value={value}
            onChange={(event) => setValue(event.target.value)}
            className="h-8 min-w-0 flex-1"
          />
          <Button type="submit" size="sm" disabled={!value || state === "sending"}>
            Send
          </Button>
        </form>
      )}
      <p className="text-micro leading-relaxed text-muted-foreground">
        {error ? (
          <span className="text-destructive">{error}</span>
        ) : (
          "It goes straight into the terminal window. The assistant never sees it, and it is not saved."
        )}
      </p>
    </div>
  )
}
