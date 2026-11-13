"use client"

import { useState } from "react"
import {
  Cross,
  LockOpen,
  SecureConnection,
  SettingsSliders,
  Shield,
  Slash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { Fail2banJail, JailConfig, JailParamResult, JailPolicy } from "@/lib/types"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { EmptyNote, EmptyState, LoadingRows, Notice } from "@/components/state"
import { ProductLogo } from "@/components/product-logo"
import { jailProduct } from "@/components/security/marks"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Field, FieldRow, FormSection } from "@/components/form"
import { VerbMenu, type Verb } from "@/components/verbs"

/** A jail's own page. */
export const jailHref = (name: string) => `/security/intrusion/jails/${encodeURIComponent(name)}`

/** The service a jail watches, on a tile: its logo, a key for sshd, a bar for the rest. */
export function JailMark({ name, className }: { name: string; className?: string }) {
  return (
    <ProductLogo
      id={jailProduct(name)}
      fallback={name === "sshd" ? SecureConnection : Slash}
      className={className}
    />
  )
}

/**
 * Releasing every address a jail holds. Not destructive on the server by
 * design: whoever earned a ban earns the next one within minutes.
 */
export function useJailRelease(onChanged: () => void) {
  const [busy, setBusy] = useState(false)
  const releaseAll = async (jail: string) => {
    setBusy(true)
    try {
      await post(`/fail2ban/${encodeURIComponent(jail)}/unban-all`, {})
      notify.success(`Released every ban in ${jail}`)
      onChanged()
    } catch (err) {
      notify.error("Could not apply", err)
    } finally {
      setBusy(false)
    }
  }
  return { busy, releaseAll }
}

/**
 * Every jail as a card you open, one line each.
 *
 * A jail is a destination: its page holds the addresses it has banned, the
 * policy it runs and the way to ban by hand, so its row takes the lit edge
 * §16 gives to things you take. The row is one height whatever the jail
 * holds. Its counts stand in columns at the row's end so a run of jails is
 * scanned down them, and its state is one word. It used to carry a band of
 * four readings and a meter under its line, which made a jail a row and a
 * half and said again what the tiles over it had said.
 */
export function JailsPanel({
  jails,
  canManage,
  clientIp,
  onChanged,
}: {
  jails: Fail2banJail[]
  canManage: boolean
  /** The address this browser arrived from, offered to the allowlist by name. */
  clientIp?: string
  onChanged: () => void
}) {
  const [tuning, setTuning] = useState<string | null>(null)
  const { busy, releaseAll } = useJailRelease(onChanged)

  return (
    <>
      <Panel plain>
        <PanelHeader
          title="Jails"
          actions={
            jails.length > 0 ? (
              <span className="numeric text-hint text-muted-foreground">{jails.length}</span>
            ) : undefined
          }
        />
        <PanelBody flush className="pt-3">
          {jails.length === 0 ? (
            <EmptyState
              icon={Slash}
              title="No jails configured"
              description="A running fail2ban with no jails bans nobody. Enable at least the sshd jail."
            />
          ) : (
            <ChoiceList className="animate-rise">
              {jails.map((jail) => {
                const verbs: Verb[] = [
                  {
                    key: "tune",
                    label: "Tune",
                    icon: SettingsSliders,
                    run: () => setTuning(jail.name),
                  },
                  {
                    key: "release",
                    label: "Release every ban",
                    icon: LockOpen,
                    disabled: busy || jail.bannedIps.length === 0,
                    run: () => void releaseAll(jail.name),
                  },
                ]
                return (
                  <ChoiceRow
                    key={jail.name}
                    verb={jail.name}
                    href={jailHref(jail.name)}
                    leading={<JailMark name={jail.name} />}
                    title={jail.name}
                    description={
                      <span className="font-mono">
                        {jail.fileList.join(", ") || "no log named"}
                      </span>
                    }
                    trailing={
                      <>
                        <JailFigure value={jail.currentlyFailed} label="failing now" />
                        <JailFigure value={jail.totalBanned} label="bans in total" />
                        <Status
                          className="w-28 max-sm:w-auto"
                          tone={jail.currentlyBanned > 0 ? "warning" : "running"}
                          label={
                            jail.currentlyBanned > 0
                              ? `${jail.currentlyBanned} banned now`
                              : "Watching"
                          }
                        />
                      </>
                    }
                    actions={
                      canManage ? (
                        <VerbMenu verbs={verbs} label={`Actions for ${jail.name}`} />
                      ) : undefined
                    }
                  />
                )
              })}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>

      {tuning && (
        <JailTuning
          jail={tuning}
          clientIp={clientIp}
          open
          onOpenChange={(o) => !o && setTuning(null)}
          onSaved={onChanged}
        />
      )}
    </>
  )
}

/**
 * One of a jail's counts, in a fixed measure so a column of cards lines up
 * the way a table's columns do. It goes quiet at zero: a column of grey
 * noughts is not a signal.
 */
function JailFigure({ value, label }: { value: number; label: string }) {
  return (
    <span className="flex w-24 shrink-0 flex-col items-end max-md:hidden">
      <span
        className={cn(
          "numeric text-body leading-5 font-medium",
          value === 0 && "text-muted-foreground",
        )}
      >
        {value.toLocaleString()}
      </span>
      <span className="text-hint leading-4 text-muted-foreground">{label}</span>
    </span>
  )
}

/**
 * What the jail's numbers and actions mean together: the rule as a sentence,
 * what it reads, what each action does with a ban, and — for sshd — whether
 * the ban lands on the port sshd actually listens on. Values the running
 * server holds that the dashboard's drop-in would change at the next start
 * are marked, since a restart is when that difference stops being invisible.
 */
export function JailPolicySection({ policy }: { policy: PollState<JailPolicy> }) {
  const { data, error, loading } = policy
  return (
    <Panel plain aria-label="Policy">
      <PanelHeader title="Policy" />
      <PanelBody flush className="space-y-3 pt-3">
        {loading && !data ? (
          <LoadingRows rows={4} hold />
        ) : error && !data ? (
          <EmptyNote>The policy could not be read.</EmptyNote>
        ) : data?.error ? (
          <EmptyNote>{data.error}</EmptyNote>
        ) : data ? (
          <>
            {data.findings.map((finding) => (
              <Notice
                key={finding.text}
                tone={finding.level === "critical" ? "danger" : "warning"}
                title={finding.level === "critical" ? "Bans here do not stop the traffic" : "Check"}
              >
                {finding.text}
              </Notice>
            ))}
            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-6 gap-y-2.5 text-body">
              <dt className="text-muted-foreground">Rule</dt>
              <dd className="min-w-0 font-medium">{data.rule}.</dd>
              <dt className="text-muted-foreground">Reads</dt>
              <dd className="min-w-0 font-mono text-hint break-words">
                {data.watches.kind === "files"
                  ? data.watches.files?.join(", ")
                  : data.watches.kind === "journal"
                    ? `journal: ${data.watches.match}`
                    : "nothing"}
              </dd>
              {data.actions.map((action) => (
                <ActionLine key={action.name} action={action} />
              ))}
              {data.coverage && (
                <>
                  <dt className="text-muted-foreground">sshd ports</dt>
                  <dd className="flex flex-wrap gap-2">
                    {data.coverage.listening.map((port) => (
                      <Tag
                        key={port}
                        mono
                        tone={data.coverage!.uncovered.includes(port) ? "danger" : undefined}
                      >
                        {port} {data.coverage!.uncovered.includes(port) ? "not covered" : "covered"}
                      </Tag>
                    ))}
                  </dd>
                </>
              )}
              {data.values.map((value) => (
                <PolicyValueLine key={value.key} value={value} />
              ))}
            </dl>
          </>
        ) : null}
      </PanelBody>
    </Panel>
  )
}

function ActionLine({ action }: { action: JailPolicy["actions"][number] }) {
  return (
    <>
      <dt className="font-mono text-hint text-muted-foreground">{action.name}</dt>
      <dd className="min-w-0">
        <Status
          verdict={action.enforces ? "ok" : "warning"}
          label={action.enforces ? action.kind : "blocks nothing"}
        />{" "}
        <span className="text-hint text-muted-foreground">{action.words}</span>
      </dd>
    </>
  )
}

function PolicyValueLine({ value }: { value: JailPolicy["values"][number] }) {
  return (
    <>
      <dt className="font-mono text-hint text-muted-foreground">{value.key}</dt>
      <dd className="numeric">
        {value.running}
        {value.dropIn && (
          <span
            className={cn("ml-2 text-hint", value.drift ? "text-warning" : "text-muted-foreground")}
          >
            {value.drift ? `restart loads ${value.dropIn}` : "kept in the drop-in"}
          </span>
        )}
      </dd>
    </>
  )
}

/**
 * One jail's policy: the three numbers that decide whether it is doing
 * anything.
 *
 * A dashboard that can only unban one address at a time is a viewer with a
 * button. How many failures, in what window, for how long is the whole policy,
 * and it lives in a file whose layout differs by distribution.
 * fail2ban-client can set them on the running server, which is both easier to
 * get right and honest about what is in force now; the same values are then
 * written to a drop-in under jail.d so they survive a restart.
 */
export function JailTuning({
  jail,
  clientIp,
  open,
  onOpenChange,
  onSaved,
}: {
  jail: string
  clientIp?: string
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { data, refresh } = usePoll<JailConfig>(
    (signal) => get(`/fail2ban/${encodeURIComponent(jail)}/config`, undefined, signal),
    0,
    [jail],
    { enabled: open },
  )
  const [pending, setPending] = useState<Record<string, string>>({})
  const [ignore, setIgnore] = useState("")
  const [busy, setBusy] = useState(false)

  const value = (key: keyof JailConfig, fallback: number) =>
    pending[key.toLowerCase()] ?? String((data?.[key] as number | undefined) ?? fallback)

  const save = async () => {
    setBusy(true)
    try {
      // One request for the whole policy: the three numbers are one setting —
      // this many failures inside this window earns this long a ban — and
      // sending them separately leaves a jail half-tuned if the tab closes.
      const params: Record<string, number> = {}
      for (const [param, raw] of Object.entries(pending)) {
        const parsed = Number(raw)
        if (Number.isFinite(parsed)) params[param] = parsed
      }
      const res = await post<JailParamResult>(`/fail2ban/${encodeURIComponent(jail)}/config`, {
        params,
      })
      if (res.warning) {
        notify.warning(`${jail} partly updated`, { description: res.warning })
      } else {
        notify.success(`${jail} updated`, {
          description: res.persisted
            ? `Applied now and written to ${res.file}, so it survives a restart.`
            : undefined,
        })
      }
      setPending({})
      refresh()
      onSaved()
    } catch (err) {
      notify.error("Could not update the jail", err)
    } finally {
      setBusy(false)
    }
  }

  const allowlist = async (ip: string) => {
    try {
      await post(`/fail2ban/${encodeURIComponent(jail)}/ignore`, { ip, add: true })
      setIgnore("")
      refresh()
    } catch (err) {
      notify.error("Could not allowlist", err)
    }
  }

  const clientListed = Boolean(clientIp && data?.ignoreIp.includes(clientIp))

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="md"
      title={<>Tune {jail}</>}
      description="This many failures inside this window earns a ban of this length."
      footer={
        <>
          <span className="flex-1" />
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button
            onClick={save}
            pending={busy}
            disabled={busy || Object.keys(pending).length === 0}
          >
            Apply
          </Button>
        </>
      }
    >
      <div className="grid gap-4">
        <div className="flex items-center gap-3">
          <ProductLogo
            id={jailProduct(jail)}
            fallback={jail === "sshd" ? SecureConnection : Slash}
          />
          <div>
            <span className="text-title font-medium">{jail}</span>
            <p className="text-hint text-muted-foreground">fail2ban jail</p>
          </div>
        </div>
        <FormSection title="Ban policy">
          <FieldRow columns={3}>
            <NumberField
              label="Failures"
              hint="before a ban"
              value={value("maxRetry", 5)}
              onChange={(v) => setPending((p) => ({ ...p, maxretry: v }))}
            />
            <NumberField
              label="Window"
              hint="seconds"
              value={value("findTime", 600)}
              onChange={(v) => setPending((p) => ({ ...p, findtime: v }))}
            />
            <NumberField
              label="Ban for"
              hint="seconds"
              value={value("banTime", 600)}
              onChange={(v) => setPending((p) => ({ ...p, bantime: v }))}
            />
          </FieldRow>
        </FormSection>

        <FormSection title="Never ban">
          <div className="flex items-center justify-between gap-2">
            {/* The address this browser arrived from, by name. Banning
                yourself is the commonest way to lose a server you were in the
                middle of hardening, and the allowlist is the control that
                stops it — so the one address it most needs is one press. */}
            {clientIp && !clientListed && (
              <Button size="xs" variant="outline" onClick={() => allowlist(clientIp)}>
                <Shield className="size-3" />
                Never ban my address
              </Button>
            )}
          </div>
          <div className="flex flex-wrap gap-1.5">
            {data?.ignoreIp.map((ip) => (
              <Tag key={ip} mono>
                {ip}
                {ip === clientIp && <span className="text-muted-foreground/70">(you)</span>}
                <button
                  type="button"
                  aria-label={`Stop allowlisting ${ip}`}
                  className="text-muted-foreground hover:text-destructive"
                  onClick={async () => {
                    try {
                      await post(`/fail2ban/${encodeURIComponent(jail)}/ignore`, {
                        ip,
                        add: false,
                      })
                      refresh()
                    } catch (err) {
                      notify.error("Could not remove", err)
                    }
                  }}
                >
                  <Cross className="size-3" />
                </button>
              </Tag>
            ))}
            {data?.ignoreIp.length === 0 && (
              <EmptyNote className="py-2">Nothing allowlisted.</EmptyNote>
            )}
          </div>
          <div className="flex items-center gap-2">
            <Input
              value={ignore}
              onChange={(e) => setIgnore(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && ignore && allowlist(ignore)}
              placeholder="203.0.113.9 or 10.0.0.0/8"
              aria-label="Address or range to never ban"
              className="font-mono text-xs"
            />
            <Button
              size="sm"
              variant="outline"
              onClick={() => allowlist(ignore)}
              disabled={!ignore}
            >
              Add
            </Button>
          </div>
        </FormSection>

        <Notice title="Applied now, and kept">
          The change goes to the running fail2ban at once and is written to a drop-in under{" "}
          <code className="font-mono">/etc/fail2ban/jail.d</code>, which fail2ban reads last — so it
          survives a restart and the distribution&rsquo;s own jail.conf is left alone.
        </Notice>
      </div>
    </Modal>
  )
}

function NumberField({
  label,
  hint,
  value,
  onChange,
}: {
  label: string
  hint: string
  value: string
  onChange: (value: string) => void
}) {
  return (
    <Field label={label} hint={hint}>
      <Input
        value={value}
        inputMode="numeric"
        aria-label={label}
        onChange={(e) => onChange(e.target.value)}
      />
    </Field>
  )
}
