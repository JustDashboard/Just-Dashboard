"use client"

import { useState } from "react"
import { del, post, put } from "@/lib/api"
import { calendarDate, relativeTime } from "@/lib/format"
import { flag } from "@/lib/countries"
import { notify } from "@/lib/toast"
import type {
  BlocklistPreview,
  ProtectionBlocklist,
  ProtectionException,
  ProtectionView,
} from "@/lib/types"
import { Globe, ListUnordered, Pencil, RefreshClockwise, Trash } from "@/components/icons"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { useConfirm, type ConfirmRequest } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FormNote } from "@/components/form"
import { DimActions } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { ProductLogo } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useAuth } from "@/hooks/use-auth"
import { CountryPicker } from "@/components/network/protection/country-picker"
import { compact, splitList, totalWord } from "@/components/network/gateway/reading"
import {
  BlocklistPreviewView,
  Geography,
  ListStanding,
  Provenance,
  SessionRevoke,
} from "@/components/network/protection/blocklist-detail"
import {
  blocklistRequest,
  countriesWord,
  coverageShort,
  feedName,
  fetchWords,
  isFetched,
  listSource,
  listState,
  REFRESH_CHOICES,
  refreshWord,
  type BlocklistRequest,
} from "@/components/network/protection/reading"

type Kind = ProtectionBlocklist["kind"]

/** A list's mark: the first country's flag on the tile a product takes, else a glyph for its kind. */
function ListMark({ list }: { list: Pick<ProtectionBlocklist, "kind" | "countries"> }) {
  if (list.kind === "country" && list.countries[0]) {
    return (
      <span
        aria-hidden
        className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-hairline bg-background text-base leading-none"
      >
        {flag(list.countries[0])}
      </span>
    )
  }
  return <ProductLogo size="sm" fallback={list.kind === "feed" ? Globe : ListUnordered} />
}

/** The confirmation that removes a list, asked from its row's menu and from its editor. */
function removal(list: ProtectionBlocklist, done: () => void): ConfirmRequest {
  return {
    title: `Remove ${list.name}`,
    confirmLabel: "Remove",
    description: (
      <p>
        The {list.count.toLocaleString()} networks in it are let through again and the list is
        forgotten.
      </p>
    ),
    action: async () => {
      await del(`/network/protection/blocklists/${list.id}`)
      notify.success("Blocklist removed")
      done()
      return "reported"
    },
  }
}

/**
 * Every blocklist as a lit card that opens its editor, one line and one
 * height whatever the list carries: what it holds and how much address space
 * that is on the line under its name, then in columns when it was fetched and
 * when it next is, how many networks it came to and what it dropped, the one
 * state worth saying (`listState`: a failed fetch in red before a stale copy
 * before a verified set), and the switch that puts it in force.
 *
 * The coverage, the fetch's error and the enforcement evidence each hung
 * under the row as a line of their own, so a list with all three stood five
 * lines taller than one with none. The error's own words, the evidence and
 * everything the columns have no room for are in the editor the row opens.
 *
 * Refresh, Edit and Remove are the row's menu. While a list is being fetched
 * its card runs a light round its edge.
 */
export function BlocklistList({
  lists,
  presets,
  onOpen,
  onChanged,
}: {
  lists: ProtectionBlocklist[]
  presets: ProtectionView["presets"]
  onOpen: (list: ProtectionBlocklist) => void
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const { can } = useAuth()
  const admin = can("system.admin")
  const [refreshing, setRefreshing] = useState<number>()
  const [switching, setSwitching] = useState<number>()
  const refresh = async (list: ProtectionBlocklist) => {
    setRefreshing(list.id)
    try {
      await post(`/network/protection/blocklists/${list.id}/refresh`)
      notify.success(`${list.name} refreshed`)
    } catch (err) {
      notify.error(`${list.name} was not refreshed`, err)
    } finally {
      setRefreshing(undefined)
      onChanged()
    }
  }
  const setEnabled = async (list: ProtectionBlocklist, enabled: boolean) => {
    setSwitching(list.id)
    try {
      await put(
        `/network/protection/blocklists/${list.id}`,
        blocklistRequest(list, presets, enabled),
      )
      notify.success(enabled ? `${list.name} is on` : `${list.name} is off`)
      onChanged()
    } catch (err) {
      notify.error(`${list.name} was not changed`, err)
    } finally {
      setSwitching(undefined)
    }
  }
  const toggle = (list: ProtectionBlocklist, enabled: boolean) => {
    if (enabled) return void setEnabled(list, true)
    confirm({
      title: `Switch off ${list.name}`,
      confirmLabel: "Switch off",
      description: (
        <p>
          The {list.count.toLocaleString()} networks in it are let through again until it is
          switched on. The list is kept.
        </p>
      ),
      action: async () => {
        await put(
          `/network/protection/blocklists/${list.id}`,
          blocklistRequest(list, presets, false),
        )
        notify.success(`${list.name} is off`)
        onChanged()
      },
    })
  }
  const verbs = (list: ProtectionBlocklist): Verb[] => [
    ...(isFetched(list)
      ? [
          {
            key: "refresh",
            label: "Refresh now",
            icon: RefreshClockwise,
            disabled: refreshing !== undefined,
            run: () => void refresh(list),
          },
        ]
      : []),
    { key: "edit", label: "Edit", icon: Pencil, run: () => onOpen(list) },
    ...(can("destructive")
      ? [
          {
            key: "remove",
            label: "Remove",
            icon: Trash,
            danger: true,
            run: () => confirm(removal(list, onChanged)),
          },
        ]
      : []),
  ]
  return (
    <>
      <ChoiceList>
        {lists.map((list, index) => {
          const state = listState(list)
          const fetch = fetchWords(list, relativeTime)
          const figures = `${list.count.toLocaleString()} networks · ${compact(list.packets)} dropped`
          return (
            <ChoiceRow
              key={list.id}
              index={index}
              busy={refreshing === list.id}
              leading={<ListMark list={list} />}
              title={
                <span className={list.enabled ? undefined : "text-muted-foreground"}>
                  {list.name}
                </span>
              }
              verb={admin ? `Edit ${list.name}` : `Open ${list.name}`}
              description={
                <>
                  <span className="max-sm:hidden">
                    {/* Its own line box is the letters' height, or the mono
                        face's taller one makes a feed's row a pixel taller. */}
                    <span className={list.kind === "feed" ? "font-mono leading-none" : undefined}>
                      {listSource(list)}
                    </span>
                    {coverageShort(list.coverage) && ` · ${coverageShort(list.coverage)}`}
                  </span>
                  {/* The columns start at `sm`; on a phone the state leads the
                      line and the figures follow it. The switch beside it
                      already says in force or off, unless there is none. */}
                  <span className="numeric sm:hidden">
                    {(!state.plain || !admin) && (
                      <>
                        <span className={STATE_INK[state.tone]}>{state.label}</span>
                        {" · "}
                      </>
                    )}
                    {figures}
                  </span>
                </>
              }
              trailing={
                <span className="flex items-center gap-4">
                  <span
                    className="hidden w-44 text-right leading-tight xl:grid"
                    title={refreshWord(list, relativeTime)}
                  >
                    <span className="truncate text-hint">{fetch?.fetched ?? "kept as typed"}</span>
                    {/* Held when there is nothing to say, so the line above
                        sits where it does on every row. */}
                    <span className="truncate font-mono text-micro text-muted-foreground">
                      {fetch?.next || "\u00a0"}
                    </span>
                  </span>
                  <span
                    className="numeric hidden w-24 text-right leading-tight sm:grid"
                    title={totalWord(list.total, calendarDate)}
                  >
                    <span className="text-body font-medium">{list.count.toLocaleString()}</span>
                    <span className="font-mono text-micro text-muted-foreground">
                      {compact(list.packets)} dropped
                    </span>
                  </span>
                  <span className="hidden w-36 md:flex" title={list.error || undefined}>
                    <Status tone={state.tone} label={state.label} />
                  </span>
                  {admin && (
                    <Switch
                      checked={list.enabled}
                      // Switching off gives protection away, which the server
                      // asks the destructive capability for.
                      disabled={switching === list.id || (list.enabled && !can("destructive"))}
                      onCheckedChange={(next) => toggle(list, next)}
                      aria-label={`${list.name} in force`}
                    />
                  )}
                </span>
              }
              actions={
                admin && (
                  <DimActions>
                    <VerbMenu verbs={verbs(list)} label={`${list.name} actions`} />
                  </DimActions>
                )
              }
              onSelect={() => onOpen(list)}
            />
          )
        })}
      </ChoiceList>
      {dialog}
    </>
  )
}

const STATE_INK = {
  running: "text-success",
  stopped: "text-muted-foreground",
  warning: "text-warning",
  danger: "text-destructive",
} as const

/**
 * Grey rows at a card's height, for a list whose first read is still out: as
 * many as it held at the last visit, so the sections under it start where
 * they will.
 */
export function CardRowsSkeleton({ rows }: { rows: number }) {
  return (
    <ul className="flex min-w-0 flex-col gap-2" aria-hidden>
      {Array.from({ length: rows }, (_, i) => (
        <li key={i}>
          <Skeleton className="h-[3.625rem] w-full rounded-xl" />
        </li>
      ))}
    </ul>
  )
}

const KINDS: { kind: Kind; title: string; hint: string }[] = [
  {
    kind: "country",
    title: "Countries",
    hint: "Every network a country is given, fetched and kept up to date.",
  },
  {
    kind: "feed",
    title: "A feed",
    hint: "A published list of bad networks, re-read daily.",
  },
  {
    kind: "manual",
    title: "Addresses I type",
    hint: "Your own addresses and networks, kept as typed.",
  },
]

/**
 * The editor for one blocklist, new or pressed. A new one starts by the kind
 * of list it is — countries, a feed, addresses typed — because the kind decides
 * every field after it and is fixed once the list exists. A country or a feed
 * is fetched before it is saved, which can take a few seconds, so the command
 * says so; a refusal (an address that would cut the reader off, a feed that
 * cannot be read) is drawn in the form.
 */
export function BlocklistModal({
  list,
  presets,
  exceptions = [],
  onOpenChange,
  onSaved,
}: {
  list?: ProtectionBlocklist
  presets: ProtectionView["presets"]
  /** The exceptions scoped to this list, said in its editor. */
  exceptions?: ProtectionException[]
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { can } = useAuth()
  // A reader opens a list to read what its row has no room for; nothing in
  // the form answers them, so nothing in it looks as if it would.
  const writable = can("system.admin")
  const { confirm, dialog } = useConfirm()
  const initial = list ? blocklistRequest(list, presets) : undefined
  const [kind, setKind] = useState<Kind>(list?.kind ?? "country")
  const [name, setName] = useState(list?.name ?? "")
  const [countries, setCountries] = useState(initial?.countries ?? [])
  const [preset, setPreset] = useState(initial ? initial.preset : (presets[0]?.id ?? ""))
  const [url, setUrl] = useState(initial?.url ?? "")
  const [entries, setEntries] = useState(list?.entries.join("\n") ?? "")
  const [refresh, setRefresh] = useState(list?.refresh || "24h")
  const [signatureUrl, setSignatureUrl] = useState(list?.signatureUrl ?? "")
  const [publicKey, setPublicKey] = useState("")
  // A preview belongs to the body it was asked for; any edit retires it.
  const [previewed, setPreviewed] = useState<{ key: string; preview: BlocklistPreview }>()
  const [previewing, setPreviewing] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const suggestion =
    kind === "country"
      ? countriesWord(countries, 3).replaceAll(" · ", ", ")
      : kind === "feed"
        ? feedName(presets, preset, url)
        : "Blocked addresses"
  const typed = splitList(entries)
  const ready =
    (name.trim() || suggestion) &&
    (kind === "country"
      ? countries.length > 0
      : kind === "feed"
        ? Boolean(preset || url.trim())
        : typed.length > 0)
  // A signed feed keeps its key on the server; the field is filled only to
  // change it, and left empty it keeps the one already pinned.
  const signed = kind === "feed" && !preset && Boolean(signatureUrl.trim())
  const body = (): BlocklistRequest => ({
    name: (name.trim() || suggestion).slice(0, 64),
    kind,
    entries: kind === "manual" ? typed : [],
    countries: kind === "country" ? countries.map((c) => c.toLowerCase()) : [],
    url: kind === "feed" && !preset ? url.trim() : "",
    preset: kind === "feed" ? preset : "",
    ...(kind === "manual" ? {} : { refresh }),
    ...(signed ? { signatureUrl: signatureUrl.trim(), publicKey: publicKey.trim() } : {}),
  })
  const bodyKey = JSON.stringify(body())
  const preview = previewed?.key === bodyKey ? previewed.preview : undefined
  const runPreview = async () => {
    setPreviewing(true)
    const key = bodyKey
    try {
      const result = await post<BlocklistPreview>("/network/protection/preview", {
        id: list?.id ?? 0,
        list: body(),
      })
      setPreviewed({ key, preview: result })
    } catch (err) {
      setPreviewed({
        key,
        preview: {
          valid: false,
          error: err instanceof Error ? err.message : String(err),
          networks: 0,
          coverage: {
            ipv4Addresses: 0,
            ipv4Share: 0,
            ipv6Slash48s: 0,
            ipv4Networks: 0,
            ipv6Networks: 0,
          },
          sources: [],
          trustedOverlap: [],
          localOverlap: [],
          impacts: [],
        },
      })
    } finally {
      setPreviewing(false)
    }
  }
  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      if (list) await put(`/network/protection/blocklists/${list.id}`, body())
      else await post("/network/protection/blocklists", body())
      notify.success(list ? "Blocklist saved" : "Blocklist made")
      onOpenChange(false)
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  const remove = () => {
    if (!list) return
    confirm(
      removal(list, () => {
        onOpenChange(false)
        onSaved()
      }),
    )
  }

  return (
    <>
      <Modal
        open
        onOpenChange={(next) => !busy && onOpenChange(next)}
        size="lg"
        title={list ? list.name : "New blocklist"}
        description="Networks this server refuses before anything answers"
        actions={
          list &&
          writable &&
          can("destructive") && (
            <Button size="sm" variant="outline" onClick={remove}>
              Remove
            </Button>
          )
        }
        footer={
          writable ? (
            <>
              {kind !== "manual" && (
                <FormNote className="mr-auto max-sm:hidden">Fetched before it is saved.</FormNote>
              )}
              <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
                Cancel
              </Button>
              <Button type="submit" form="blocklist-form" disabled={!ready || busy} pending={busy}>
                {list ? "Save list" : "Make list"}
              </Button>
            </>
          ) : (
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              Close
            </Button>
          )
        }
      >
        <form
          id="blocklist-form"
          className="flex min-w-0 flex-col gap-5"
          onSubmit={(event) => {
            event.preventDefault()
            if (writable && ready && !busy) void submit()
          }}
        >
          {list ? (
            <ListStanding list={list} presets={presets} />
          ) : (
            <ChoiceGrid className="sm:grid-cols-3">
              {KINDS.map((k) => (
                <ChoiceCard key={k.kind} selected={kind === k.kind} onClick={() => setKind(k.kind)}>
                  <ChoiceCardTitle>{k.title}</ChoiceCardTitle>
                  <ChoiceCardHint>{k.hint}</ChoiceCardHint>
                </ChoiceCard>
              ))}
            </ChoiceGrid>
          )}

          {/* One fieldset, so a reader's copy of the form is switched off in
              one place; it lays its fields out as the form does. */}
          <fieldset disabled={!writable} className="flex min-w-0 flex-col gap-5">
            {kind === "country" && (
              <Field label="Countries" hint="Everything these countries are allocated is refused.">
                <CountryPicker value={countries} onChange={setCountries} />
              </Field>
            )}

            {kind === "feed" && (
              <>
                <ChoiceGrid className="sm:grid-cols-3">
                  {presets.map((p) => (
                    <ChoiceCard
                      key={p.id}
                      selected={preset === p.id}
                      onClick={() => setPreset(p.id)}
                    >
                      <ChoiceCardTitle>{p.name}</ChoiceCardTitle>
                      <ChoiceCardHint className="line-clamp-3">{p.description}</ChoiceCardHint>
                    </ChoiceCard>
                  ))}
                  <ChoiceCard selected={preset === ""} onClick={() => setPreset("")}>
                    <ChoiceCardTitle>Another feed</ChoiceCardTitle>
                    <ChoiceCardHint>Any address that serves one network per line.</ChoiceCardHint>
                  </ChoiceCard>
                </ChoiceGrid>
                {preset === "" && (
                  <Field label="Feed address" htmlFor="blocklist-url" hint="An https address.">
                    <Input
                      id="blocklist-url"
                      value={url}
                      placeholder="https://example.net/blocklist.txt"
                      onChange={(event) => setUrl(event.target.value)}
                      className="font-mono"
                      autoComplete="off"
                    />
                  </Field>
                )}
              </>
            )}

            {kind === "feed" && preset === "" && (
              <>
                <Field
                  label="Signature address"
                  htmlFor="blocklist-signature"
                  hint="Optional: an https file holding a detached Ed25519 signature of the list, base64 or hex."
                >
                  <Input
                    id="blocklist-signature"
                    value={signatureUrl}
                    placeholder="https://example.net/blocklist.txt.sig"
                    onChange={(event) => setSignatureUrl(event.target.value)}
                    className="font-mono"
                    autoComplete="off"
                  />
                </Field>
                {signatureUrl.trim() && (
                  <Field
                    label="Publisher's public key"
                    htmlFor="blocklist-key"
                    hint={
                      list?.integrity === "signed" && signatureUrl.trim() === list.signatureUrl
                        ? "A key is pinned. Leave this empty to keep it, or give a new one."
                        : "The 32-byte Ed25519 key in base64. Every fetch must verify against it."
                    }
                  >
                    <Input
                      id="blocklist-key"
                      value={publicKey}
                      placeholder="base64"
                      onChange={(event) => setPublicKey(event.target.value)}
                      className="font-mono"
                      autoComplete="off"
                    />
                  </Field>
                )}
              </>
            )}

            {kind !== "manual" && (
              <Field
                label="Fetched again"
                htmlFor="blocklist-refresh"
                hint="After a failure it retries sooner, backing off."
              >
                <Select value={refresh} onValueChange={setRefresh}>
                  <SelectTrigger id="blocklist-refresh" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {REFRESH_CHOICES.map((c) => (
                      <SelectItem key={c.value} value={c.value}>
                        {c.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )}

            {kind === "manual" && (
              <Field
                label="Addresses and networks"
                htmlFor="blocklist-entries"
                hint={`One per line or comma-separated: 198.51.100.7, 203.0.113.0/24. ${typed.length.toLocaleString()} so far.`}
              >
                <Textarea
                  id="blocklist-entries"
                  value={entries}
                  rows={6}
                  placeholder={"198.51.100.7\n203.0.113.0/24"}
                  onChange={(event) => setEntries(event.target.value)}
                  className="font-mono"
                  spellCheck={false}
                />
              </Field>
            )}

            <Field
              label="Name"
              htmlFor="blocklist-name"
              hint={
                suggestion && !name.trim()
                  ? `Called “${suggestion}” if you leave it empty.`
                  : "What it is called in the list."
              }
            >
              <Input
                id="blocklist-name"
                value={name}
                placeholder={suggestion || "Blocked networks"}
                onChange={(event) => setName(event.target.value)}
                autoComplete="off"
              />
            </Field>
          </fieldset>

          {writable && (
            <div className="flex flex-wrap items-center gap-2">
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => void runPreview()}
                disabled={!ready || previewing}
                pending={previewing}
              >
                Preview what it blocks
              </Button>
              {kind !== "manual" && (
                <span className="text-hint text-muted-foreground">
                  Fetches the list to look; nothing is saved or loaded.
                </span>
              )}
            </div>
          )}
          {preview && <BlocklistPreviewView preview={preview} />}
          {!preview && kind === "country" && countries.length > 0 && (
            <Geography
              geography={{
                source:
                  "ipdeny.com aggregated zones, built from the regional internet registries' delegation files.",
                basis:
                  "A country list is the address blocks registered to organisations in that country, not where a machine physically is.",
                limits: [
                  "Cloud, CDN, VPN and mobile networks carry one country's addresses elsewhere, so legitimate visitors abroad can be refused and attackers can use another country.",
                ],
                countries,
              }}
            />
          )}

          {list && (list.sources?.length ?? 0) > 0 && <Provenance sources={list.sources ?? []} />}
          {list && exceptions.length > 0 && (
            <p className="text-hint text-muted-foreground">
              Excepted from it: {exceptions.map((e) => `${e.address} (${e.reason})`).join("; ")}.
              Change these under Exceptions.
            </p>
          )}
          {list && list.enabled && writable && <SessionRevoke list={list} />}

          {error && (
            <p role="alert" className="animate-rise text-body text-destructive">
              {error}
            </p>
          )}
        </form>
      </Modal>
      {dialog}
    </>
  )
}
