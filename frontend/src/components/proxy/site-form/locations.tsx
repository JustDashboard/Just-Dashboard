"use client"

import { Plus, Trash } from "@/components/icons"
import type { Container, Listener, RequestLimit, LocationMatch, SiteLocation } from "@/lib/types"
import { Disclosure, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Group } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { nothingListening } from "@/components/proxy/upstream-options"
import { UpstreamPicker } from "@/components/proxy/upstream-picker"
import {
  MATCH_LABEL,
  renderedPath,
  renderedUpstream,
  splitUpstream,
} from "@/components/proxy/site-routes"
import { Field, ListField, type Picker, OnOffField } from "@/components/proxy/site-form/fields"

/**
 * Extra paths, each sent somewhere other than the site's default.
 *
 * The commonest reverse-proxy layout after "one app on one domain" is two:
 * /api to a backend and everything else to a static build, or /ws to a socket
 * server. The renderer has always supported it and the form did not, which
 * meant the one arrangement past the simplest sent people to the raw editor.
 *
 * nginx matches the longest prefix regardless of order, so these are rendered
 * before the catch-all purely because that is the order a reader expects.
 */
export function LocationsField({
  locations,
  onChange,
  picker,
  sso,
}: {
  locations: SiteLocation[]
  onChange: (locations: SiteLocation[]) => void
  picker: Picker
  /** Whether the site signs in on every path or only on chosen ones. */
  sso?: "site" | "paths"
}) {
  const update = (i: number, patch: Partial<SiteLocation>) =>
    onChange(locations.map((loc, j) => (j === i ? { ...loc, ...patch } : loc)))

  return (
    <div className="space-y-2">
      {locations.map((loc, i) => (
        <LocationCard
          key={i}
          index={i}
          loc={loc}
          update={(patch) => update(i, patch)}
          remove={() => onChange(locations.filter((_, j) => j !== i))}
          picker={picker}
          sso={sso}
        />
      ))}
      <Button
        size="sm"
        variant="outline"
        onClick={() => onChange([...locations, { path: "", upstream: "", webSockets: false }])}
      >
        <Plus className="size-3.5" />
        Add a path
      </Button>
    </div>
  )
}

const MATCHES: LocationMatch[] = ["", "^~", "=", "~", "~*"]

/** One extra path: how it matches, where it goes, and what it does differently. */
function LocationCard({
  index,
  loc,
  update,
  remove,
  picker,
  sso,
}: {
  index: number
  loc: SiteLocation
  update: (patch: Partial<SiteLocation>) => void
  remove: () => void
  picker: Picker
  sso?: "site" | "paths"
}) {
  const id = `site-loc-${index}`
  const match = loc.match ?? ""
  const prefix = match === "" || match === "^~"
  const regex = match === "~" || match === "~*"
  const folder = !loc.upstream && Boolean(loc.root)
  const forwards = Boolean(loc.upstream)
  const shown = renderedPath(loc)
  const facts = [
    loc.bodyLimit && `uploads ${loc.bodyLimit}`,
    loc.timeout && `${loc.timeout}s timeout`,
    loc.buffering && `buffering ${loc.buffering}`,
    loc.requestBuffering && `upload buffering ${loc.requestBuffering}`,
    loc.basicAuthFile && "own password",
    sso === "site" && loc.forwardAuth === "off" && "no sign-in",
    sso === "paths" && loc.forwardAuth === "on" && "signs in",
    (loc.allowFrom?.length || loc.denyFrom?.length) && "own address list",
  ].filter(Boolean)
  // An exact or regex path cannot serve a folder with alias, so a folder
  // there is nginx's root, which adds the path to it.
  const setMatch = (next: LocationMatch) =>
    update({
      match: next || undefined,
      ...(next === "" || next === "^~"
        ? {}
        : { stripPrefix: false, spa: false, ...(loc.root ? { rootMode: "root" as const } : {}) }),
    })
  return (
    <Group className="space-y-2">
      <div className="flex items-center gap-2">
        <Select
          value={match || "prefix"}
          onValueChange={(v) => setMatch(v === "prefix" ? "" : (v as LocationMatch))}
        >
          <SelectTrigger aria-label="Match" className="w-44 shrink-0">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {MATCHES.map((m) => (
              <SelectItem key={m || "prefix"} value={m || "prefix"}>
                {MATCH_LABEL[m]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Input
          value={loc.path}
          onChange={(e) => update({ path: e.target.value })}
          placeholder={regex ? "\\.(png|jpg)$" : "/api"}
          aria-label="Path"
          className="font-mono text-xs"
        />
        <IconAction
          label={`Remove ${loc.path || "location"}`}
          className="text-destructive"
          onClick={remove}
        >
          <Trash />
        </IconAction>
      </div>
      <UpstreamPicker
        value={loc.upstream ?? ""}
        onChange={(upstream) => update({ upstream, root: "" })}
        options={picker.options}
        loading={picker.loading}
        failed={picker.failed}
        onRetry={picker.onRetry}
        placeholder={
          regex
            ? "http://127.0.0.1:4000 — no path after the port"
            : "http://127.0.0.1:4000 — or leave empty and give a folder"
        }
        label="Upstream"
        pickLabel={`Pick from running services for ${loc.path || "this path"}`}
      />
      {loc.upstream && (
        <IdleNote
          upstream={loc.upstream}
          listeners={picker.listeners}
          containers={picker.containers}
        />
      )}
      {!loc.upstream && (
        <Input
          value={loc.root ?? ""}
          onChange={(e) =>
            update({ root: e.target.value, ...(prefix ? {} : { rootMode: "root" as const }) })
          }
          placeholder="/var/www/assets"
          aria-label="Folder"
          className="font-mono text-xs"
        />
      )}
      {folder && loc.rootMode === "root" && (
        <FormNote>
          Files come from{" "}
          <code className="font-mono">
            {loc.root?.replace(/\/$/, "")}
            {prefix ? loc.path : "<the request's path>"}
          </code>
          : nginx adds the path to the folder.
          {prefix && (
            <>
              {" "}
              <button
                type="button"
                className="underline underline-offset-2 hover:text-foreground"
                onClick={() => update({ rootMode: undefined })}
              >
                Serve the folder itself at {loc.path || "the path"}
              </button>
            </>
          )}
        </FormNote>
      )}
      {forwards && prefix && (
        <label className="flex items-center gap-2 text-hint text-muted-foreground">
          <Checkbox
            checked={loc.stripPrefix ?? false}
            onCheckedChange={(v) => update({ stripPrefix: Boolean(v) })}
          />
          Strip {loc.path || "the path"} before forwarding
          {loc.stripPrefix && loc.path && (
            <span className="font-mono">
              ({shown.replace(/\/$/, "")}/users arrives as {splitUpstream(renderedUpstream(loc))[1]}
              users)
            </span>
          )}
        </label>
      )}
      {folder && prefix && (
        <label className="flex items-center gap-2 text-hint text-muted-foreground">
          <Checkbox
            checked={loc.spa ?? false}
            onCheckedChange={(v) => update({ spa: Boolean(v) })}
          />
          Single-page app: a path with no file gets {shown}index.html
        </label>
      )}
      {forwards && (
        <label className="flex items-center gap-2 text-hint text-muted-foreground">
          <Checkbox
            checked={loc.webSockets}
            onCheckedChange={(v) => update({ webSockets: Boolean(v) })}
          />
          WebSockets on this path
        </label>
      )}
      <label className="flex items-center gap-2 text-hint text-muted-foreground">
        <Checkbox
          checked={Boolean(loc.rateLimit)}
          onCheckedChange={(v) =>
            update({ rateLimit: v ? { rate: "10r/m", burst: 5 } : undefined })
          }
        />
        A request rate of its own
      </label>
      {loc.rateLimit && (
        <RequestLimitFields
          id={`${id}-limit`}
          value={loc.rateLimit}
          onChange={(rateLimit) => update({ rateLimit })}
        />
      )}
      <Disclosure quiet summary="Advanced" facts={facts.length > 0 ? facts.join(" · ") : undefined}>
        <div className="space-y-3 pt-2">
          {forwards && (
            <>
              <FieldRow>
                <Field label="Upload limit" htmlFor={`${id}-body`} tip="Empty keeps the site's.">
                  <Input
                    id={`${id}-body`}
                    value={loc.bodyLimit ?? ""}
                    onChange={(e) => update({ bodyLimit: e.target.value || undefined })}
                    placeholder="200m"
                    className="font-mono text-xs"
                  />
                </Field>
                <Field
                  label="Timeout"
                  htmlFor={`${id}-timeout`}
                  tip="Seconds; empty keeps the site's."
                >
                  <Input
                    id={`${id}-timeout`}
                    value={loc.timeout ? String(loc.timeout) : ""}
                    inputMode="numeric"
                    onChange={(e) => update({ timeout: Number(e.target.value) || undefined })}
                    placeholder="300"
                    className="font-mono text-xs"
                  />
                </Field>
              </FieldRow>
              <FieldRow>
                <OnOffField
                  id={`${id}-buffering`}
                  label="Response buffering"
                  value={loc.buffering}
                  onChange={(buffering) => update({ buffering })}
                />
                <OnOffField
                  id={`${id}-request-buffering`}
                  label="Upload buffering"
                  value={loc.requestBuffering}
                  onChange={(requestBuffering) => update({ requestBuffering })}
                />
              </FieldRow>
            </>
          )}
          {sso && (
            <OptionList>
              <OptionRow
                title={sso === "site" ? "Answer without signing in" : "Sign in here"}
                hint={
                  sso === "site"
                    ? "For a public path, or the auth server's own when it is on this site."
                    : "Visitors sign in through the site's auth server before this path answers."
                }
                checked={loc.forwardAuth === (sso === "site" ? "off" : "on")}
                onCheckedChange={(on) =>
                  update({ forwardAuth: on ? (sso === "site" ? "off" : "on") : undefined })
                }
              />
            </OptionList>
          )}
          {!sso && (
            <Field
              label="Password file"
              htmlFor={`${id}-auth`}
              tip="In place of the site's on this path. Empty keeps the site's."
            >
              <Input
                id={`${id}-auth`}
                value={loc.basicAuthFile ?? ""}
                onChange={(e) => update({ basicAuthFile: e.target.value || undefined })}
                placeholder="/etc/nginx/jd-auth/api"
                list="site-auth-files"
                className="font-mono text-hint"
              />
            </Field>
          )}
          <ListField
            id={`${id}-allow`}
            label="Allow only these"
            placeholder="10.0.0.0/8"
            values={loc.allowFrom ?? []}
            onChange={(allowFrom) => update({ allowFrom })}
            hint="A list here replaces the site's on this path — nginx uses only one."
          />
          <ListField
            id={`${id}-deny`}
            label="Deny"
            placeholder="203.0.113.0/24"
            values={loc.denyFrom ?? []}
            onChange={(denyFrom) => update({ denyFrom })}
          />
        </div>
      </Disclosure>
    </Group>
  )
}

/** Said under a path's upstream when nothing listens behind it. */
function IdleNote({
  upstream,
  listeners,
  containers,
}: {
  upstream: string
  listeners: Listener[] | undefined
  containers: Container[] | undefined
}) {
  const idle = nothingListening(upstream, listeners, containers)
  return idle ? <FormNote tone="warning">{idle}</FormNote> : null
}

/** A rate as its number and its unit, the two halves nginx writes as 10r/s. */
function splitRate(rate: string): [string, "s" | "m"] {
  const m = /^(\d*)r\/([sm])$/.exec(rate)
  return m ? [m[1], m[2] as "s" | "m"] : [rate, "s"]
}

/** A request rate, its burst, and what it counts by. */
export function RequestLimitFields({
  id,
  value,
  onChange,
}: {
  id: string
  value: RequestLimit
  onChange: (value: RequestLimit) => void
}) {
  const [count, unit] = splitRate(value.rate)
  const patch = (next: Partial<RequestLimit>) => onChange({ ...value, ...next })
  return (
    <div className="space-y-2">
      <FieldRow>
        <Field label="Requests" htmlFor={`${id}-rate`} tip="Per client, at a steady pace.">
          <div className="flex gap-2">
            <Input
              id={`${id}-rate`}
              value={count}
              inputMode="numeric"
              onChange={(e) => patch({ rate: `${e.target.value.trim()}r/${unit}` })}
              className="font-mono text-xs"
            />
            <Select value={unit} onValueChange={(u) => patch({ rate: `${count}r/${u}` })}>
              <SelectTrigger aria-label="Per" className="w-36 shrink-0">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="s">per second</SelectItem>
                <SelectItem value="m">per minute</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </Field>
        <Field
          label="Burst"
          htmlFor={`${id}-burst`}
          tip="Requests past the rate that wait their turn instead of getting a 429."
        >
          <Input
            id={`${id}-burst`}
            value={String(value.burst ?? 0)}
            inputMode="numeric"
            onChange={(e) => patch({ burst: Number(e.target.value) || 0 })}
            className="font-mono text-xs"
          />
        </Field>
      </FieldRow>
      <Field label="Counted" htmlFor={`${id}-key`}>
        <Select
          value={value.key ?? "ip"}
          onValueChange={(k) => patch({ key: k === "ip_path" ? "ip_path" : undefined })}
        >
          <SelectTrigger id={`${id}-key`} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="ip">Per address</SelectItem>
            <SelectItem value="ip_path">Per address and path</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      <label className="flex items-center gap-2 text-hint text-muted-foreground">
        <Checkbox
          checked={value.noDelay ?? false}
          onCheckedChange={(v) => patch({ noDelay: Boolean(v) })}
        />
        Answer the burst at once rather than spacing it out
      </label>
    </div>
  )
}
