"use client"

import { useState } from "react"
import { Cross, LockOpen, SettingsSliders, Shield, Slash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { Fail2banJail, JailConfig, JailParamResult } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useMediaQuery } from "@/hooks/use-mobile"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { EmptyNote, EmptyState, Notice } from "@/components/state"
import { DimActions, IconAction } from "@/components/icon-action"
import { SidePanel } from "@/components/side-panel"
import { ProductLogo } from "@/components/product-logo"
import { jailProduct } from "@/components/security/marks"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

/**
 * Every jail as a card you open, with the addresses it is holding one press
 * away.
 *
 * A jail was a row in a table, and before that a card in a two-column grid
 * whose boxes were all one size whatever was in them. It is a card again, of
 * the shape the Backups and Docker pages settled on: every one of these opens
 * the jail's own sheet, so it is a destination and takes the lit edge §16
 * gives to things you take — and its readings, which the table lined up in
 * columns, keep a fixed measure each on a wide screen so a column of cards is
 * still scanned down the way the table was. Beneath its name the jail says
 * what it watches, and its mark is the service it watches for: nginx's for
 * `nginx-http-auth`, a glyph for `sshd`, which has no mark to draw.
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
  const [open, setOpen] = useState<string | null>(null)
  const [tuning, setTuning] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [banning, setBanning] = useState("")

  const act = async (fn: () => Promise<unknown>, ok: string) => {
    setBusy(true)
    try {
      await fn()
      notify.success(ok)
      onChanged()
    } catch (err) {
      notify.error("Could not apply", err)
    } finally {
      setBusy(false)
    }
  }

  const selected = jails.find((j) => j.name === open)
  // On a phone the readings go under the name, which otherwise had the width
  // the readings and the verbs left it — none. Chosen once rather than drawn
  // twice and hidden, so each reading is in the document once.
  const wide = useMediaQuery("(min-width: 640px)")

  // A ban by hand. The route has been on the server since the jail controls
  // shipped and nothing on the page reached it — so the address that kept
  // appearing in the log could be blocked forever at the firewall, or not at
  // all, with no ten-minute answer in between.
  const ban = () => {
    if (!selected || !banning.trim()) return
    const ip = banning.trim()
    void act(async () => {
      await post(`/fail2ban/${encodeURIComponent(selected.name)}/ban`, { ip })
      setBanning("")
    }, `${ip} banned in ${selected.name}`)
  }

  return (
    <>
      <Panel plain>
        <PanelHeader
          title="Jails"
          actions={
            jails.length > 0 ? (
              <span className="numeric text-hint text-muted-foreground">
                {jails.length === 1 ? "1 jail" : `${jails.length} jails`}
              </span>
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
              {jails.map((jail, index) => (
                <ChoiceRow
                  key={jail.name}
                  index={index}
                  verb={jail.name}
                  onSelect={() => setOpen(jail.name)}
                  leading={<ProductLogo id={jailProduct(jail.name)} size="sm" fallback={Slash} />}
                  title={jail.name}
                  description={
                    <span className="font-mono">{jail.fileList.join(", ") || "no log named"}</span>
                  }
                  trailing={
                    wide ? (
                      <>
                        <JailReading
                          label="banned now"
                          value={jail.currentlyBanned}
                          tone="warning"
                        />
                        <JailReading label="failing" value={jail.currentlyFailed} />
                        <JailReading label="bans in total" value={jail.totalBanned} muted />
                      </>
                    ) : (
                      <JailReading label="banned now" value={jail.currentlyBanned} tone="warning" />
                    )
                  }
                  actions={
                    canManage ? (
                      <DimActions>
                        <IconAction
                          label={`Tune ${jail.name}`}
                          onClick={() => setTuning(jail.name)}
                        >
                          <SettingsSliders />
                        </IconAction>
                        <IconAction
                          label={`Release every ban in ${jail.name}`}
                          disabled={busy || jail.bannedIps.length === 0}
                          onClick={() =>
                            act(
                              () =>
                                post(`/fail2ban/${encodeURIComponent(jail.name)}/unban-all`, {}),
                              `Released every ban in ${jail.name}`,
                            )
                          }
                        >
                          <LockOpen />
                        </IconAction>
                      </DimActions>
                    ) : undefined
                  }
                >
                  {!wide && (
                    <div className="flex min-w-0 flex-wrap items-center gap-x-5 gap-y-1 text-hint text-muted-foreground">
                      <JailReading label="failing" value={jail.currentlyFailed} />
                      <JailReading label="bans in total" value={jail.totalBanned} muted />
                      <JailReading label="failures in total" value={jail.totalFailed} muted />
                    </div>
                  )}
                </ChoiceRow>
              ))}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>

      <SidePanel
        open={Boolean(selected)}
        onOpenChange={(o) => !o && setOpen(null)}
        width="sm"
        title={selected?.name ?? ""}
        description={
          selected
            ? `${selected.currentlyBanned} banned now · ${selected.totalBanned} in total · ${selected.currentlyFailed} failing`
            : undefined
        }
        actions={
          canManage &&
          selected && (
            <>
              <Button size="xs" variant="outline" onClick={() => setTuning(selected.name)}>
                <SettingsSliders className="size-3.5" />
                Tune
              </Button>
              <Button
                size="xs"
                variant="ghost"
                disabled={busy || selected.bannedIps.length === 0}
                onClick={() =>
                  act(
                    () => post(`/fail2ban/${encodeURIComponent(selected.name)}/unban-all`, {}),
                    `Released every ban in ${selected.name}`,
                  )
                }
              >
                Release all
              </Button>
            </>
          )
        }
      >
        <div className="space-y-4">
          {canManage && selected && (
            <div className="space-y-1.5">
              <Label htmlFor="jail-ban-address">Ban an address now</Label>
              <div className="flex gap-2">
                <Input
                  id="jail-ban-address"
                  value={banning}
                  onChange={(e) => setBanning(e.target.value)}
                  onKeyDown={(e) => e.key === "Enter" && ban()}
                  placeholder="203.0.113.9"
                  className="font-mono text-xs"
                />
                <Button
                  size="sm"
                  variant="outline"
                  onClick={ban}
                  disabled={busy || !banning.trim()}
                >
                  <Slash className="size-3.5" />
                  Ban
                </Button>
              </div>
              <p className="text-hint leading-relaxed text-muted-foreground">
                For this jail&rsquo;s ban time, the same as an earned ban. Your own address is
                refused. A block that should outlive the ban is a firewall rule, from the offenders
                list below.
              </p>
            </div>
          )}

          <div>
            <p className="eyebrow mb-1">Banned now</p>
            {selected && selected.bannedIps.length === 0 ? (
              <p className="text-body leading-relaxed text-muted-foreground">
                Nothing currently banned. Bans expire, so an empty list is not the same as a quiet
                night — the ban activity on the page behind this is the record.
              </p>
            ) : (
              <div className="divide-y divide-hairline">
                {selected?.bannedIps.map((ip) => (
                  <div key={ip} className="flex items-center justify-between gap-2 py-1.5">
                    <span className="truncate font-mono text-xs">{ip}</span>
                    {/* Always visible, not revealed on hover: in a table the
                        actions are an aside to the data, but this sheet was opened
                        *to* release an address, so hiding the release button until
                        the pointer finds the row hides the only thing here. */}
                    {canManage && (
                      <span className="flex shrink-0 items-center gap-0.5">
                        <IconAction
                          label={`Unban ${ip}`}
                          disabled={busy}
                          onClick={() =>
                            act(
                              () =>
                                post(`/fail2ban/${encodeURIComponent(selected.name)}/unban`, {
                                  ip,
                                }),
                              `${ip} unbanned`,
                            )
                          }
                        >
                          <LockOpen />
                        </IconAction>
                        <IconAction
                          label={`Never ban ${ip} again`}
                          disabled={busy}
                          onClick={() =>
                            act(
                              () =>
                                post(`/fail2ban/${encodeURIComponent(selected.name)}/ignore`, {
                                  ip,
                                  add: true,
                                }),
                              `${ip} added to the allowlist`,
                            )
                          }
                        >
                          <Shield />
                        </IconAction>
                      </span>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      </SidePanel>

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
 * the way the table's columns did. It goes quiet at zero — a column of grey
 * noughts is not a signal — and a count of addresses held right now takes the
 * warning tone, because that is the reading the page is opened for.
 */
function JailReading({
  label,
  value,
  tone,
  muted,
}: {
  label: string
  value: number
  /** The tone the figure takes when it is not zero. */
  tone?: "warning"
  /** A lifetime total, quieter than the live counts beside it. */
  muted?: boolean
}) {
  const active = value > 0 && !muted
  return (
    <span className="inline-flex min-w-0 items-baseline gap-1 whitespace-nowrap sm:w-28">
      <span
        className={cn(
          "numeric text-xs",
          active ? "font-medium" : "text-muted-foreground",
          active && tone === "warning" && "text-warning",
        )}
      >
        {value}
      </span>
      <span className="text-micro text-muted-foreground">{label}</span>
    </span>
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
function JailTuning({
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
    pending[key] ?? String((data?.[key] as number | undefined) ?? fallback)

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
      size="sm"
      title={<>Tune {jail}</>}
      description="This many failures inside this window earns a ban of this length."
      footer={
        <>
          <span className="flex-1" />
          <Button onClick={save} disabled={busy || Object.keys(pending).length === 0}>
            Apply
          </Button>
        </>
      }
    >
      <div className="grid gap-4">
        <div className="grid gap-3 sm:grid-cols-3">
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
        </div>

        <div className="space-y-1.5">
          <div className="flex items-center justify-between gap-2">
            <Label>Never ban</Label>
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
          <div className="flex gap-2">
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
        </div>

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
    <div className="space-y-1.5">
      <Label>{label}</Label>
      <Input
        value={value}
        inputMode="numeric"
        aria-label={label}
        onChange={(e) => onChange(e.target.value)}
      />
      <p className="text-hint text-muted-foreground">{hint}</p>
    </div>
  )
}
