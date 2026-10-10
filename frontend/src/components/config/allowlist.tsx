"use client"

import { useState } from "react"
import { Cross, Globe, Router, Terminal, type Icon } from "@/components/icons"
import { networkOf } from "@/lib/clients"
import { Field } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

/** An address with an optional prefix length, v4 or v6; the server has the last word. */
const CIDR = /^(?:\d{1,3}(?:\.\d{1,3}){3}|[0-9a-f:]*:[0-9a-f:.]*)(?:\/\d{1,3})?$/i

/**
 * The network allowlist as the networks it lets in, one row each, drawn as
 * where those addresses are: the tailnet as Tailscale, loopback as the way an
 * SSH tunnel arrives, a private range as the local network.
 *
 * It was one text field holding `100.64.0.0/10,127.0.0.1/32`, which is the
 * setting exactly as `.env` stores it and nothing a reader can check at a
 * glance — and this is the field that, wrong, makes the dashboard stop
 * existing for whoever is reading it. The value is still that comma list;
 * this only draws it, and adds or removes one entry of it at a time.
 */
export function Allowlist({
  value,
  onChange,
  disabled,
}: {
  value: string
  onChange: (value: string) => void
  disabled?: boolean
}) {
  const [adding, setAdding] = useState("")
  const entries = value
    .split(",")
    .map((entry) => entry.trim())
    .filter(Boolean)
  const candidate = adding.trim()
  const valid = CIDR.test(candidate) && !entries.includes(candidate)
  // The server refuses a list that no longer lets 127.0.0.1 in, so the entry
  // that does is not offered for removal while it is the only one.
  const loopback = entries.filter(coversLoopback)

  const add = () => {
    if (!valid) return
    onChange([...entries, candidate].join(","))
    setAdding("")
  }

  return (
    <Field
      label="Network allowlist"
      htmlFor="cfg-cidr-add"
      hint="Checked before the login page. Keep 127.0.0.1/32."
      info="The allowlist is enforced before authentication, so removing your own network does not give you an error page — it makes the dashboard stop existing for you. Loopback is the way back in over an SSH tunnel."
    >
      <RowList aria-label="Allowed networks">
        {entries.map((entry) => {
          const kind = kindOf(entry)
          return (
            <Row
              key={entry}
              className="py-2"
              leading={<ProductLogo id={kind.product} fallback={kind.glyph} size="sm" />}
              title={<CidrText cidr={entry} />}
              subtitle={
                kind.warning ? <span className="text-warning">{kind.label}</span> : kind.label
              }
              trailing={
                !disabled &&
                !(loopback.length === 1 && loopback[0] === entry) && (
                  <IconAction
                    label={`Remove ${entry}`}
                    onClick={() => onChange(entries.filter((other) => other !== entry).join(","))}
                  >
                    <Cross />
                  </IconAction>
                )
              }
            />
          )
        })}
      </RowList>
      {!disabled && (
        <form
          className="flex min-w-0 gap-2 pt-1"
          onSubmit={(event) => {
            event.preventDefault()
            add()
          }}
        >
          <Input
            id="cfg-cidr-add"
            value={adding}
            placeholder="Add a network, e.g. 192.168.1.0/24"
            onChange={(event) => setAdding(event.target.value)}
            className="font-mono text-body"
          />
          <Button type="submit" variant="outline" disabled={!valid}>
            Add
          </Button>
        </form>
      )}
    </Field>
  )
}

function coversLoopback(entry: string) {
  return entry.startsWith("127.") || entry.endsWith("/0")
}

function CidrText({ cidr }: { cidr: string }) {
  const [address, prefix] = cidr.split("/")
  return (
    <span className="numeric font-mono">
      {address}
      {prefix !== undefined && <span className="text-[var(--tag-pink)]">/{prefix}</span>}
    </span>
  )
}

/** Where a range's addresses are, as `networkOf` reads its first one. */
function kindOf(cidr: string): { label: string; product?: string; glyph: Icon; warning?: boolean } {
  const [address, prefix] = cidr.split("/")
  if (prefix === "0") return { label: "every address there is", glyph: Globe, warning: true }
  const network = networkOf(address)
  switch (network.kind) {
    case "tailscale":
      return { label: "your tailnet", product: "tailscale", glyph: Globe }
    case "server":
      return { label: "this server · how an SSH tunnel arrives", glyph: Terminal }
    case "local":
      return { label: "local network", glyph: Router }
    default:
      return { label: "public addresses", glyph: Globe }
  }
}
