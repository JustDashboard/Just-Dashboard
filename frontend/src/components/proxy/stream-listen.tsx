"use client"

import type { StreamAddress, StreamModule, StreamSpec } from "@/lib/types"
import { Field, FormNote, OptionList, OptionRow } from "@/components/form"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type StreamListenFields = Pick<
  StreamSpec,
  | "listen"
  | "listenEnd"
  | "samePort"
  | "address"
  | "protocol"
  | "acceptProxy"
  | "trustedProxies"
  | "routes"
>

/** The select's value for every address; either family, as the IPv6 switch says. */
const EVERY = "every"

/** The widest range the server takes. */
const MAX_RANGE = 100

/**
 * Where a stream listens beyond its port: one of this host's addresses or
 * all of them, with or without IPv6; a range of ports, each to the same port
 * on the backend if asked; and a listener behind a load balancer that sends
 * the PROXY header itself.
 */
export function StreamListen({
  spec,
  addresses,
  module,
  error,
  onChange,
}: {
  spec: StreamListenFields
  /** This host's addresses, as the server read them. */
  addresses: StreamAddress[]
  module: StreamModule
  /** The save's refusal of the address, when that is what it refused. */
  error?: string
  onChange: (change: Partial<StreamSpec>, field?: string) => void
}) {
  const every = !spec.address || spec.address === "0.0.0.0"
  const listed = addresses.some((a) => a.address === spec.address)
  const ranged = spec.listenEnd !== undefined
  const width = ranged && spec.listenEnd ? spec.listenEnd - spec.listen + 1 : 0
  const tcp = spec.protocol === "tcp"
  const proxyBlocker = !tcp
    ? "TCP only — nginx takes no PROXY header on UDP."
    : module.realip === false && module.state !== "unknown"
      ? "This nginx was built without stream_realip_module, so it cannot take the client's address from the header."
      : undefined

  return (
    <div className="space-y-4">
      <Field
        label="Address"
        hint={
          every
            ? "Every address this host has, including any added later."
            : spec.address === "::"
              ? "Every IPv6 address, as the file has it — it has no IPv4 listen."
              : listed || addresses.length === 0
                ? `Only reachable on ${spec.address}.`
                : `${spec.address} is not an address of this host, so nginx could not listen on it.`
        }
        error={error}
      >
        <Select
          value={every ? EVERY : spec.address}
          onValueChange={(v) => onChange({ address: v === EVERY ? undefined : v }, "spec.address")}
        >
          <SelectTrigger className="w-full" aria-label="Listening address">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={EVERY}>Every address</SelectItem>
            {spec.address === "::" && <SelectItem value="::">Every IPv6 address</SelectItem>}
            {spec.address && spec.address !== "::" && !every && !listed && (
              <SelectItem value={spec.address} hint="as the file has it">
                {spec.address}
              </SelectItem>
            )}
            {addresses.map((a) => (
              <SelectItem key={a.address} value={a.address} hint={a.interface}>
                {a.address}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>

      <OptionList>
        {every && (
          <OptionRow
            title="Listen on IPv6 too"
            hint="Off, nginx listens on IPv4 addresses only, and a client reaching this host over IPv6 is refused."
            checked={!spec.address}
            onCheckedChange={(v) =>
              onChange({ address: v ? undefined : "0.0.0.0" }, "spec.address")
            }
          />
        )}
        <OptionRow
          title="A range of ports"
          hint={`nginx listens on every port from ${spec.listen || "the one above"} to the last, as one stream — a game server's block of ports. At most ${MAX_RANGE}.`}
          checked={ranged}
          onCheckedChange={(v) =>
            onChange(
              // A total cap would count each port of a range on its own, so a
              // range takes none.
              {
                listenEnd: v ? 0 : undefined,
                samePort: v ? spec.samePort : undefined,
                ...(v && { maxConnTotal: undefined }),
              },
              "spec.listen",
            )
          }
        >
          <Field
            label="Last port"
            htmlFor="stream-listen-end"
            hint={
              width > MAX_RANGE
                ? `That is ${width} ports; a range takes at most ${MAX_RANGE}.`
                : width > 1
                  ? `${width} ports.`
                  : `A port above ${spec.listen || "the first"}.`
            }
          >
            <Input
              id="stream-listen-end"
              value={spec.listenEnd || ""}
              inputMode="numeric"
              onChange={(e) => onChange({ listenEnd: Number(e.target.value) || 0 }, "spec.listen")}
              placeholder={spec.listen ? String(spec.listen + 15) : "27030"}
              className="font-mono text-xs"
            />
          </Field>
        </OptionRow>
        {ranged && (
          <OptionRow
            title="Same port on the backend"
            hint={
              spec.routes
                ? "Routing by TLS name sends each name to a port of its own, so it cannot keep the port."
                : "A client on 27020 reaches the backend on 27020. The upstream is then the backend's IP address alone, one server."
            }
            checked={Boolean(spec.samePort)}
            disabled={!spec.samePort && Boolean(spec.routes)}
            onCheckedChange={(v) => onChange({ samePort: v || undefined })}
          />
        )}
        <OptionRow
          title="Behind a load balancer that sends PROXY"
          hint={
            (!spec.acceptProxy && proxyBlocker) ||
            "Every client has to send the PROXY header, so one connecting straight to this port is closed. From the load balancers below, the client the header names is the one access rules, caps and the traffic log see."
          }
          checked={Boolean(spec.acceptProxy)}
          disabled={!spec.acceptProxy && Boolean(proxyBlocker)}
          onCheckedChange={(v) =>
            onChange({
              acceptProxy: v || undefined,
              trustedProxies: v ? (spec.trustedProxies ?? []) : undefined,
            })
          }
        >
          <Field
            label="Trusted load balancers"
            htmlFor="stream-trusted-proxies"
            hint="One address or CIDR per line. A peer not listed keeps its own address, whatever its header says."
          >
            <Textarea
              id="stream-trusted-proxies"
              value={(spec.trustedProxies ?? []).join("\n")}
              onChange={(e) => onChange({ trustedProxies: e.target.value.split("\n") })}
              rows={3}
              placeholder="10.0.0.2"
              className="font-mono text-xs"
            />
          </Field>
          {proxyBlocker && <FormNote>{proxyBlocker}</FormNote>}
        </OptionRow>
      </OptionList>
    </div>
  )
}
