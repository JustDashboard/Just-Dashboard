"use client"

import { useId, useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { errorMessage, post } from "@/lib/api"
import {
  DNS_ENGINES,
  DNS_SERVICE_BASE,
  readDNSProvision,
  type DNSEngine,
  type DNSProvisionRequest,
  type DNSServiceProvision,
} from "@/lib/network-dns-services"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { Field, FieldRow, OptionRow } from "@/components/form"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"

export function DNSServiceProvisionForm({
  onReviewed,
}: {
  onReviewed: (value: DNSServiceProvision) => void
}) {
  const { can } = useAuth()
  const id = useId()
  const [engine, setEngine] = useState<DNSEngine>("adguard")
  const [name, setName] = useState("")
  const [managementPort, setManagementPort] = useState("18080")
  const [dnsPort, setDNSPort] = useState("1053")
  const [memory, setMemory] = useState("256")
  const [cpus, setCPUs] = useState("0.5")
  const [upstreams, setUpstreams] = useState("")
  const [management, setManagement] = useState(false)
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const managementValue = Number(managementPort)
  const dnsValue = Number(dnsPort)
  const memoryValue = Number(memory)
  const cpuValue = Number(cpus)
  const portProblem = (value: number) => !Number.isInteger(value) || value < 1024 || value > 65535
  const passwordMax = engine === "adguard" ? 72 : 4096
  const endpoints = upstreams
    .split(/[\n,]+/)
    .map((value) => value.trim())
    .filter(Boolean)
  const valid =
    can("system.admin") &&
    name.trim().length > 0 &&
    name.trim().length <= 80 &&
    !portProblem(managementValue) &&
    !portProblem(dnsValue) &&
    managementValue !== dnsValue &&
    Number.isInteger(memoryValue) &&
    memoryValue >= 128 &&
    memoryValue <= 1024 &&
    Number.isFinite(cpuValue) &&
    cpuValue >= 0.25 &&
    cpuValue <= 2 &&
    endpoints.length > 0 &&
    endpoints.length <= 16 &&
    password.length >= 16 &&
    new TextEncoder().encode(password).length <= passwordMax &&
    (engine !== "adguard" || username.trim().length > 0)

  const stage = async () => {
    if (!valid || busy) return
    const request: DNSProvisionRequest = {
      name: name.trim(),
      engine,
      managementPort: managementValue,
      dnsPort: dnsValue,
      memoryMiB: memoryValue,
      cpus: cpuValue,
      upstreams: endpoints,
      management,
      username: engine === "adguard" ? username.trim() : "admin",
      password,
    }
    setBusy(true)
    setError(undefined)
    setPassword("")
    try {
      onReviewed(readDNSProvision(await post(DNS_SERVICE_BASE + "/provisions", request)))
    } catch (err) {
      setError(`${errorMessage(err)} Re-enter the bootstrap password to review again.`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <form
      className="space-y-5"
      onSubmit={(event) => {
        event.preventDefault()
        void stage()
      }}
    >
      <ChoiceGrid columns={3}>
        {DNS_ENGINES.map((item) => (
          <ChoiceCard
            key={item.value}
            selected={engine === item.value}
            disabled={busy}
            title={item.name}
            verb={`Choose ${item.name}`}
            onClick={() => setEngine(item.value)}
          />
        ))}
      </ChoiceGrid>
      <Field htmlFor={`${id}-name`} label="Service name">
        <Input
          id={`${id}-name`}
          value={name}
          maxLength={80}
          onChange={(event) => setName(event.target.value)}
          disabled={busy}
        />
      </Field>
      <FieldRow>
        <Field
          htmlFor={`${id}-management-port`}
          label="Loopback management port"
          hint="127.0.0.1 · an unused port from 1024 to 65535"
          error={portProblem(managementValue) ? "Choose a port from 1024 to 65535." : undefined}
        >
          <Input
            id={`${id}-management-port`}
            inputMode="numeric"
            value={managementPort}
            onChange={(event) => setManagementPort(event.target.value)}
            disabled={busy}
          />
        </Field>
        <Field
          htmlFor={`${id}-dns-port`}
          label="Loopback DNS port"
          hint="Classic UDP and TCP DNS on 127.0.0.1"
          error={
            portProblem(dnsValue)
              ? "Choose a port from 1024 to 65535."
              : managementValue === dnsValue
                ? "DNS and management need different ports."
                : undefined
          }
        >
          <Input
            id={`${id}-dns-port`}
            inputMode="numeric"
            value={dnsPort}
            onChange={(event) => setDNSPort(event.target.value)}
            disabled={busy}
          />
        </Field>
      </FieldRow>
      <FieldRow>
        <Field htmlFor={`${id}-memory`} label="Memory limit (MiB)" hint="128 to 1024 MiB">
          <Input
            id={`${id}-memory`}
            inputMode="numeric"
            value={memory}
            onChange={(event) => setMemory(event.target.value)}
            disabled={busy}
          />
        </Field>
        <Field htmlFor={`${id}-cpu`} label="CPU limit" hint="0.25 to 2 cores">
          <Input
            id={`${id}-cpu`}
            inputMode="decimal"
            value={cpus}
            onChange={(event) => setCPUs(event.target.value)}
            disabled={busy}
          />
        </Field>
      </FieldRow>
      <Field
        htmlFor={`${id}-upstreams`}
        label="Initial DNS upstreams"
        hint="One reachable literal address and port per line, such as 192.0.2.53:53 or [2001:db8::53]:53. Container loopback is not a host upstream."
      >
        <Textarea
          id={`${id}-upstreams`}
          value={upstreams}
          onChange={(event) => setUpstreams(event.target.value)}
          spellCheck={false}
          disabled={busy}
          className="font-mono"
        />
      </Field>
      <FieldRow>
        <Field htmlFor={`${id}-username`} label="Native administrator username">
          <Input
            id={`${id}-username`}
            value={engine === "adguard" ? username : "admin"}
            onChange={(event) => setUsername(event.target.value)}
            disabled={busy || engine !== "adguard"}
            autoComplete="off"
          />
        </Field>
        <Field
          htmlFor={`${id}-password`}
          label="Bootstrap password"
          hint={`An explicit password of at least 16 characters${engine === "adguard" ? ", within AdGuard's 72-byte limit" : ""}`}
        >
          <Input
            id={`${id}-password`}
            type="password"
            value={password}
            maxLength={passwordMax}
            onChange={(event) => setPassword(event.target.value)}
            disabled={busy}
            autoComplete="new-password"
          />
        </Field>
      </FieldRow>
      <OptionRow
        title="Allow reviewed native changes"
        hint="Off keeps this connection read-only in the dashboard, including after setup."
        checked={management}
        onCheckedChange={setManagement}
        disabled={busy}
      />
      <Notice title="Owned service on loopback">
        The review names one container, a dedicated bridge and two persistent volumes. Only the
        reviewed cached image can be used. Removing the service also removes its saved data.
      </Notice>
      {error && (
        <div role="alert">
          <Notice tone="warning" title="Setup review unavailable">
            {error}
          </Notice>
        </div>
      )}
      <Button type="submit" disabled={!valid || busy} pending={busy}>
        Review owned DNS setup
      </Button>
    </form>
  )
}
