"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useMemo, useState } from "react"
import {
  ChoiceList,
  ChoiceRow,
  FlowActions,
  FlowHeader,
  FlowPanel,
  FlowPanelBody,
  FlowPanelHeader,
  FlowSteps,
} from "@/components/flow"
import { Field, FieldRow } from "@/components/form"
import { ArrowLeft, ArrowRight, Check, Globe } from "@/components/icons"
import { Page } from "@/components/page"
import { ProductGlyph, ProductLogos } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { Notice } from "@/components/state"
import { Segments } from "@/components/deploy/settings/segments"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { usePoll } from "@/hooks/use-poll"
import { get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import {
  candidates,
  defaultPort,
  isoOf,
  latency,
  osLabel,
  peerName,
  peerOS,
  protocolLabel,
  REMOTE_DESKTOP_HREF,
  sessionHref,
  type RdComputer,
  type RdOS,
  type RdPeer,
  type RdProtocol,
  type RdReach,
} from "@/lib/remote-desktop"
import { notify } from "@/lib/toast"
import {
  ComputerFields,
  inputOf,
  type ComputerDraft,
} from "@/components/remote-desktop/computer-form"
import { ComputerMark } from "@/components/remote-desktop/marks"
import { useRemoteDesktop } from "@/components/remote-desktop/use-remote-desktop"

const STEPS = [
  { key: "computer", label: "Computer" },
  { key: "turn-on", label: "Turn it on" },
  { key: "sign-in", label: "Sign in" },
]

const OS_OPTIONS: { value: RdOS; label: React.ReactNode }[] = [
  {
    value: "windows",
    label: (
      <>
        <ProductGlyph id="windows" className="size-3.5" />
        Windows
      </>
    ),
  },
  {
    value: "macos",
    label: (
      <>
        <ProductGlyph id="apple" className="size-3.5" />
        Mac
      </>
    ),
  },
  {
    value: "linux",
    label: (
      <>
        <ProductGlyph id="linux" className="size-3.5" />
        Linux
      </>
    ),
  },
]

/** Windows Home has no Remote Desktop server, so it is reached over VNC instead. */
type Edition = "pro" | "home"

function blankDraft(): ComputerDraft {
  return {
    name: "",
    os: "windows",
    host: "",
    protocol: "rdp",
    port: "3389",
    username: "",
    domain: "",
    password: "",
    security: "",
    layout: "",
    peerId: "",
  }
}

function protocolFor(os: RdOS, edition: Edition): RdProtocol {
  if (os === "macos") return "vnc"
  if (os === "windows" && edition === "home") return "vnc"
  return "rdp"
}

/**
 * Adding a computer, as three questions: which one, is its remote desktop
 * server on, and how does the dashboard sign in.
 *
 * A flow page (§17). The first screen offers the tailnet's own desktops,
 * drawn as their systems, so a computer is chosen rather than typed; the
 * second says exactly where the switch is on that system and watches the
 * port until it answers, which is the step a person setting this up from a
 * phone at the other end of the house most needs to see happen; the third
 * saves the sign-in and opens the screen.
 */
export function AddComputer({ peer: first }: { peer?: string }) {
  const router = useRouter()
  const { data } = useRemoteDesktop(0)
  const [step, setStep] = useState(0)
  const [draft, setDraft] = useState<ComputerDraft>(blankDraft)
  const [edition, setEdition] = useState<Edition>("pro")
  const [manual, setManual] = useState(false)
  const [saving, setSaving] = useState(false)
  const [seeded, setSeeded] = useState(false)
  const offered = useMemo(() => (data ? candidates(data.tailnet, data.computers) : []), [data])
  const change = (next: Partial<ComputerDraft>) => setDraft((held) => ({ ...held, ...next }))

  const choose = (peer: RdPeer) => {
    const os = peerOS(peer.os) ?? "windows"
    const protocol = protocolFor(os, edition)
    change({
      name: peerName(peer),
      os,
      host: peerName(peer),
      protocol,
      port: String(defaultPort(protocol)),
      peerId: peer.id,
    })
    setManual(false)
    setStep(1)
  }
  // A link from the overview's tailnet list arrives with its device chosen.
  if (!seeded && data && first) {
    setSeeded(true)
    const peer = data.tailnet.peers.find((candidate) => candidate.id === first)
    if (peer) choose(peer)
  }

  const setOS = (os: RdOS) => {
    const protocol = protocolFor(os, edition)
    change({ os, protocol, port: String(defaultPort(protocol)) })
  }
  const setWindowsEdition = (next: Edition) => {
    setEdition(next)
    const protocol = protocolFor(draft.os, next)
    change({ protocol, port: String(defaultPort(protocol)) })
  }

  const save = async () => {
    setSaving(true)
    try {
      const computer = await post<RdComputer>("/remote-desktop/computers", inputOf(draft, false))
      notify.success(`Added ${computer.name}`)
      router.push(sessionHref(computer.id))
    } catch (err) {
      notify.error("Could not add the computer", err)
      setSaving(false)
    }
  }

  const eyebrow = (
    <Link
      href={REMOTE_DESKTOP_HREF}
      className="inline-flex items-center gap-1.5 rounded-sm focus-ring hover:text-foreground"
    >
      <ArrowLeft aria-hidden className="size-3" />
      Remote Desktop
    </Link>
  )
  const questions = [
    "Which computer do you want to reach?",
    `Turn on ${protocolLabel(draft.protocol, draft.os)} on ${draft.name || "the computer"}`,
    `How does the dashboard sign in to ${draft.name || "it"}?`,
  ]

  return (
    <Page register="flow" className="animate-rise">
      <FlowHeader
        eyebrow={eyebrow}
        question={questions[step]}
        steps={<FlowSteps steps={STEPS} current={step} />}
      />
      {!data ? (
        <Skeleton className="h-72 rounded-xl" />
      ) : step === 0 ? (
        <ChooseComputer
          offered={offered}
          tailnet={data.tailnet.installed}
          manual={manual}
          draft={draft}
          onChoose={choose}
          onManual={() => setManual(true)}
          onChange={change}
          onOS={setOS}
          onNext={() => setStep(1)}
        />
      ) : step === 1 ? (
        <TurnItOn
          draft={draft}
          edition={edition}
          onEdition={setWindowsEdition}
          onBack={() => setStep(0)}
          onNext={() => setStep(2)}
        />
      ) : (
        <FlowPanel>
          <FlowPanelHeader title={draft.name || "The computer"} />
          <FlowPanelBody>
            <ComputerFields
              draft={draft}
              onChange={change}
              layouts={data.layouts}
              editing={false}
            />
          </FlowPanelBody>
          <FlowActions
            note="The password is sealed on this server and only ever sent to guacd."
            secondary={
              <Button
                variant="ghost"
                className="h-11 text-muted-foreground sm:h-9"
                onClick={() => setStep(1)}
              >
                Back
              </Button>
            }
          >
            <Button
              className="h-11 sm:h-9"
              pending={saving}
              disabled={draft.name.trim() === "" || draft.host.trim() === ""}
              onClick={() => void save()}
            >
              Save and connect
            </Button>
          </FlowActions>
        </FlowPanel>
      )}
    </Page>
  )
}

function ChooseComputer({
  offered,
  tailnet,
  manual,
  draft,
  onChoose,
  onManual,
  onChange,
  onOS,
  onNext,
}: {
  offered: RdPeer[]
  tailnet: boolean
  manual: boolean
  draft: ComputerDraft
  onChoose: (peer: RdPeer) => void
  onManual: () => void
  onChange: (next: Partial<ComputerDraft>) => void
  onOS: (os: RdOS) => void
  onNext: () => void
}) {
  if (manual) {
    return (
      <FlowPanel>
        <FlowPanelHeader title="Somewhere else" />
        <FlowPanelBody className="flex flex-col gap-4">
          <Field label="System">
            <Segments label="System" value={draft.os} options={OS_OPTIONS} onChange={onOS} />
          </Field>
          <FieldRow>
            <Field
              label="Address"
              htmlFor="computer-address"
              hint="A name or an IP address this server can reach."
            >
              <Input
                id="computer-address"
                className="font-mono"
                value={draft.host}
                onChange={(event) =>
                  onChange({ host: event.target.value, name: draft.name || event.target.value })
                }
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <Field label="Name" htmlFor="computer-name">
              <Input
                id="computer-name"
                value={draft.name}
                onChange={(event) => onChange({ name: event.target.value })}
                autoComplete="off"
              />
            </Field>
          </FieldRow>
        </FlowPanelBody>
        <FlowActions>
          <Button className="h-11 sm:h-9" disabled={draft.host.trim() === ""} onClick={onNext}>
            <ArrowRight className="size-4" />
            Continue
          </Button>
        </FlowActions>
      </FlowPanel>
    )
  }
  return (
    <div className="flex min-w-0 flex-col gap-4">
      {!tailnet && (
        <Notice title="This server is not on Tailscale">
          <p className="text-body text-muted-foreground">
            With Tailscale on this server and on the computer, the computer is reached privately
            wherever it is, and nothing is opened to the internet.{" "}
            <Link href="/network/vpn" className="font-medium text-foreground hover:underline">
              Set up Tailscale
            </Link>
          </p>
        </Notice>
      )}
      <FlowPanel>
        <FlowPanelHeader
          title="On your tailnet"
          actions={<ProductGlyph id="tailscale" className="size-4" />}
        />
        <FlowPanelBody flush>
          {offered.length === 0 ? (
            <p className="p-4 text-body text-muted-foreground">
              {tailnet
                ? "Every Windows, Mac and Linux device on the tailnet is added already. Install Tailscale on another computer and sign in with the same account, and it appears here."
                : "No tailnet to read."}
            </p>
          ) : (
            <ChoiceList className="p-2">
              {offered.map((peer, index) => {
                const os = peerOS(peer.os)!
                return (
                  <ChoiceRow
                    key={peer.id}
                    index={index}
                    leading={
                      <ComputerMark
                        os={os}
                        size="sm"
                        reach={{
                          kind: peer.online ? "open" : "offline",
                          tone: peer.online ? "success" : "neutral",
                          label: peer.online ? "Online" : "Offline",
                        }}
                      />
                    }
                    title={peerName(peer)}
                    verb={`Add ${peerName(peer)}`}
                    description={
                      <>
                        {osLabel(os)} · <span className="font-mono">{peer.address}</span> ·{" "}
                        {peer.online
                          ? peer.direct
                            ? "online, direct"
                            : `online${peer.relay ? `, via ${peer.relay}` : ""}`
                          : peer.lastSeen
                            ? `seen ${relativeTime(isoOf(peer.lastSeen))}`
                            : "offline"}
                      </>
                    }
                    onSelect={() => onChoose(peer)}
                  />
                )
              })}
            </ChoiceList>
          )}
        </FlowPanelBody>
        <FlowActions note="A computer this server reaches another way can be added by its address.">
          <Button variant="outline" className="h-11 sm:h-9" onClick={onManual}>
            <Globe className="size-4" />
            Somewhere else
          </Button>
        </FlowActions>
      </FlowPanel>
    </div>
  )
}

/**
 * Where the switch is, on this computer's system, and the port watched until
 * it answers. Nothing here changes the computer; it says what to press on it.
 */
function TurnItOn({
  draft,
  edition,
  onEdition,
  onBack,
  onNext,
}: {
  draft: ComputerDraft
  edition: Edition
  onEdition: (edition: Edition) => void
  onBack: () => void
  onNext: () => void
}) {
  const port = Number.parseInt(draft.port, 10) || defaultPort(draft.protocol)
  const probe = usePoll(
    (signal) => get<RdReach>("/remote-desktop/probe", { host: draft.host, port }, signal),
    3000,
    [draft.host, port],
  )
  const reach = probe.data
  const open = reach?.state === "open"
  const server = protocolLabel(draft.protocol, draft.os)

  return (
    <div className="grid min-w-0 gap-6 lg:grid-cols-[minmax(0,1fr)_22rem] lg:items-start">
      <FlowPanel>
        <FlowPanelHeader
          title={
            <span className="flex items-center gap-2">
              <ProductLogos ids={[draft.os === "macos" ? "apple" : draft.os]} size="sm" />
              On {draft.name || "the computer"}
            </span>
          }
          actions={
            draft.os === "windows" && (
              <Segments
                label="Windows edition"
                value={edition}
                onChange={onEdition}
                options={[
                  { value: "pro", label: "Pro or Enterprise" },
                  { value: "home", label: "Home" },
                ]}
              />
            )
          }
        />
        <FlowPanelBody>
          <ol className="flex min-w-0 flex-col gap-4 text-body">
            {instructions(draft.os, edition).map((item, index) => (
              <li key={index} className="flex min-w-0 gap-3">
                <span className="numeric flex size-6 shrink-0 items-center justify-center rounded-md border border-hairline bg-surface-sunken text-hint font-medium">
                  {index + 1}
                </span>
                <div className="min-w-0 space-y-1 pt-0.5">
                  <p className="font-medium">{item.title}</p>
                  {item.detail && <p className="text-muted-foreground">{item.detail}</p>}
                </div>
              </li>
            ))}
          </ol>
        </FlowPanelBody>
        <div
          role="status"
          aria-live="polite"
          className="flex min-w-0 items-center gap-3 border-t border-hairline px-4 py-3 text-body"
        >
          {open ? (
            <>
              <span className="flex size-5 items-center justify-center rounded-full bg-plot-success text-success">
                <Check className="size-3" />
              </span>
              <span className="min-w-0">
                <span className="font-medium">{draft.name || draft.host}</span> answers on port{" "}
                {port}
                <span className="numeric text-muted-foreground">
                  {" "}
                  · {latency(reach?.latencyMs)}
                </span>
              </span>
            </>
          ) : (
            <>
              <StatusDot tone="notice" live className="mx-1.5 size-2" />
              <span className="min-w-0 text-muted-foreground">
                Waiting for {server} on{" "}
                <span className="font-mono text-foreground">
                  {draft.host}:{port}
                </span>
                {reach?.error ? ` (${reach.error})` : ""}…
              </span>
            </>
          )}
        </div>
        <FlowActions
          secondary={
            <Button variant="ghost" className="h-11 text-muted-foreground sm:h-9" onClick={onBack}>
              Back
            </Button>
          }
          note={open ? undefined : "You can continue now and connect once it answers."}
        >
          <Button className="h-11 sm:h-9" onClick={onNext}>
            <ArrowRight className="size-4" />
            Continue
          </Button>
        </FlowActions>
      </FlowPanel>
      <aside className="flex min-w-0 flex-col gap-4 text-body">
        <div className="space-y-2">
          <p className="eyebrow">Keep it reachable</p>
          <p className="text-muted-foreground">
            A computer that sleeps cannot be reached. Turn sleep off while it is plugged in, and
            leave Tailscale signed in.
          </p>
        </div>
        <div className="space-y-2">
          <p className="eyebrow">Nothing is exposed</p>
          <p className="text-muted-foreground">
            {server} only needs to answer this server, over Tailscale. Do not forward its port on
            your router.
          </p>
        </div>
        {draft.os === "macos" && (
          <div className="space-y-2">
            <p className="eyebrow">Keep macOS updated</p>
            <p className="text-muted-foreground">
              macOS 14.8.9, 15.7.9 and 26.6.1 fix a flaw that let Screen Sharing be signed in to
              without a password.
            </p>
          </div>
        )}
      </aside>
    </div>
  )
}

type Instruction = { title: React.ReactNode; detail?: React.ReactNode }

function path(...parts: string[]) {
  return <span className="font-medium text-foreground">{parts.join(" › ")}</span>
}

function instructions(os: RdOS, edition: Edition): Instruction[] {
  if (os === "macos")
    return [
      {
        title: <>Open {path("System Settings", "General", "Sharing")}</>,
        detail: "Turn on Screen Sharing.",
      },
      {
        title: "Click the ⓘ beside Screen Sharing",
        detail: (
          <>
            Under <span className="text-foreground">Allow access for</span>, choose All users, or
            add your own account. The dashboard signs in with that account&apos;s name and the
            password you log in with.
          </>
        ),
      },
      {
        title: <>Open {path("System Settings", "Energy")}</>,
        detail: "Turn on Prevent automatic sleeping when the display is off.",
      },
    ]
  if (os === "linux")
    return [
      {
        title: "Install xrdp",
        detail: (
          <>
            On Ubuntu or Debian:{" "}
            <code className="font-mono text-foreground">sudo apt install xrdp</code>, then{" "}
            <code className="font-mono text-foreground">sudo systemctl enable --now xrdp</code>. It
            answers on port 3389 and signs in with the computer&apos;s own accounts.
          </>
        ),
      },
      {
        title: "Turn off automatic suspend",
        detail: "In the power settings, while it is plugged in.",
      },
    ]
  if (edition === "home")
    return [
      {
        title: "Install TightVNC",
        detail: (
          <>
            Windows Home has no Remote Desktop server. Download TightVNC from tightvnc.com and,
            while installing, keep{" "}
            <span className="text-foreground">Register TightVNC Server as a system service</span>{" "}
            ticked so it runs before anyone signs in.
          </>
        ),
      },
      {
        title: "Set its password for remote access",
        detail: "TightVNC asks for one while installing. You will type it on the next step.",
      },
      {
        title: <>Open {path("Settings", "System", "Power & battery", "Screen and sleep")}</>,
        detail: "Set sleep to Never while plugged in.",
      },
    ]
  return [
    {
      title: <>Open {path("Settings", "System", "Remote Desktop")}</>,
      detail: "Turn on Remote Desktop and confirm.",
    },
    {
      title: "Note the account you sign in with",
      detail: "A Microsoft account signs in with its email address and its password, not the PIN.",
    },
    {
      title: <>Open {path("Settings", "System", "Power & battery", "Screen and sleep")}</>,
      detail: "Set sleep to Never while plugged in.",
    },
    {
      title: "If the check below keeps waiting",
      detail: (
        <>
          Windows may treat the Tailscale network as public. In{" "}
          {path("Windows Defender Firewall", "Allow an app")}, tick Remote Desktop for Public
          networks too.
        </>
      ),
    },
  ]
}
