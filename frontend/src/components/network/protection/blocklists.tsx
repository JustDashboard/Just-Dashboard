"use client"

import { useState } from "react"
import { del, post, put } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { flag } from "@/lib/countries"
import { notify } from "@/lib/toast"
import type { ProtectionBlocklist, ProtectionView } from "@/lib/types"
import { Globe, ListUnordered, RefreshClockwise } from "@/components/icons"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FormFacts, FormNote } from "@/components/form"
import { IconAction, DimActions } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { ProductLogo } from "@/components/product-logo"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { useAuth } from "@/hooks/use-auth"
import { CountryPicker } from "@/components/network/protection/country-picker"
import { compact, splitList } from "@/components/network/gateway/reading"
import {
  blocklistRequest,
  countriesWord,
  feedName,
  isFetched,
  listWhat,
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

/**
 * Every blocklist as a lit card that opens its editor: what it holds (its
 * countries with their flags, the feed it is read from, the addresses typed),
 * how many networks that came to and when it was last fetched, what it has
 * dropped, and the switch that puts it in force. A fetched list carries a
 * refresh in its actions, and while one is being fetched its card runs a light
 * round its edge. An error from the last fetch is said on the card in red,
 * because a list that failed to load is a list that is not blocking.
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
  return (
    <>
      <ChoiceList>
        {lists.map((list, index) => (
          <ChoiceRow
            key={list.id}
            index={index}
            busy={refreshing === list.id}
            leading={<ListMark list={list} />}
            title={
              <span className="inline-flex min-w-0 items-center gap-2">
                <span className={list.enabled ? "truncate" : "truncate text-muted-foreground"}>
                  {list.name}
                </span>
                {list.containsYou && <Tag tone="warning">holds your address</Tag>}
              </span>
            }
            verb={`Edit ${list.name}`}
            description={
              <>
                {[
                  listWhat(list, presets),
                  isFetched(list)
                    ? list.refreshed
                      ? `fetched ${relativeTime(list.refreshed)}`
                      : "not fetched yet"
                    : undefined,
                ]
                  .filter(Boolean)
                  .join(" · ")}
                {/* The figures have a column of their own from `sm`; on a phone
                    that column would take the title's width, so they join the line. */}
                <span className="numeric sm:hidden">
                  {" · "}
                  {list.count.toLocaleString()} networks · {compact(list.packets)} dropped
                </span>
              </>
            }
            trailing={
              <span className="flex items-center gap-4">
                <span className="numeric hidden min-w-[4.5rem] text-right leading-tight sm:grid">
                  <span className="text-body font-medium">{list.count.toLocaleString()}</span>
                  <span className="font-mono text-micro text-muted-foreground">
                    {compact(list.packets)} dropped
                  </span>
                </span>
                <Switch
                  checked={list.enabled}
                  disabled={!can("system.admin") || switching === list.id}
                  onCheckedChange={(next) => toggle(list, next)}
                  aria-label={`${list.name} in force`}
                />
              </span>
            }
            actions={
              isFetched(list) && (
                <DimActions>
                  <IconAction
                    label={`Refresh ${list.name}`}
                    onClick={() => void refresh(list)}
                    disabled={!can("system.admin") || refreshing !== undefined}
                  >
                    <RefreshClockwise aria-hidden />
                  </IconAction>
                </DimActions>
              )
            }
            onSelect={() => onOpen(list)}
          >
            {list.error && <p className="text-hint text-destructive">{list.error}</p>}
          </ChoiceRow>
        ))}
      </ChoiceList>
      {dialog}
    </>
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
  onOpenChange,
  onSaved,
}: {
  list?: ProtectionBlocklist
  presets: ProtectionView["presets"]
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const initial = list ? blocklistRequest(list, presets) : undefined
  const [kind, setKind] = useState<Kind>(list?.kind ?? "country")
  const [name, setName] = useState(list?.name ?? "")
  const [countries, setCountries] = useState(initial?.countries ?? [])
  const [preset, setPreset] = useState(initial ? initial.preset : (presets[0]?.id ?? ""))
  const [url, setUrl] = useState(initial?.url ?? "")
  const [entries, setEntries] = useState(list?.entries.join("\n") ?? "")
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
  const submit = async () => {
    setBusy(true)
    setError(undefined)
    const body: BlocklistRequest = {
      name: (name.trim() || suggestion).slice(0, 64),
      kind,
      entries: kind === "manual" ? typed : [],
      countries: kind === "country" ? countries.map((c) => c.toLowerCase()) : [],
      url: kind === "feed" && !preset ? url.trim() : "",
      preset: kind === "feed" ? preset : "",
    }
    try {
      if (list) await put(`/network/protection/blocklists/${list.id}`, body)
      else await post("/network/protection/blocklists", body)
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
    confirm({
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
        onOpenChange(false)
        onSaved()
      },
    })
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
          can("destructive") && (
            <Button size="sm" variant="outline" onClick={remove}>
              Remove
            </Button>
          )
        }
        footer={
          <>
            {kind !== "manual" && (
              <FormNote className="mr-auto max-sm:hidden">Fetched before it is saved.</FormNote>
            )}
            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
              Cancel
            </Button>
            <Button
              type="submit"
              form="blocklist-form"
              disabled={!can("system.admin") || !ready || busy}
              pending={busy}
            >
              {list ? "Save list" : "Make list"}
            </Button>
          </>
        }
      >
        <form
          id="blocklist-form"
          className="flex min-w-0 flex-col gap-5"
          onSubmit={(event) => {
            event.preventDefault()
            if (ready && !busy) void submit()
          }}
        >
          {list ? (
            <FormFacts>
              <span>
                {list.kind === "country"
                  ? "Countries"
                  : list.kind === "feed"
                    ? "Feed"
                    : "Typed addresses"}{" "}
                list · the kind cannot change
              </span>
            </FormFacts>
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

          {kind === "country" && (
            <Field label="Countries" hint="Everything these countries are allocated is refused.">
              <CountryPicker value={countries} onChange={setCountries} />
            </Field>
          )}

          {kind === "feed" && (
            <>
              <ChoiceGrid className="sm:grid-cols-3">
                {presets.map((p) => (
                  <ChoiceCard key={p.id} selected={preset === p.id} onClick={() => setPreset(p.id)}>
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
