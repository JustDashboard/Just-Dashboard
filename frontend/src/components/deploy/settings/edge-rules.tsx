"use client"

import { useState } from "react"
import Link from "next/link"
import { ArrowDown, ArrowUp, Plus, Trash } from "@/components/icons"
import { ApiError, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentEdgeAction,
  DeploymentEdgeMethod,
  DeploymentEdgeRule,
  DeploymentEnvironmentConfiguration,
  DeploymentRateLimit,
  RequestFacet,
} from "@/lib/types"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Well } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { ListField } from "@/components/proxy/site-form"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { useConfiguration, useSettingDraft } from "@/components/deploy/settings/use-configuration"
import { useProject } from "@/components/deploy/project-context"
import {
  EDGE_ACTIONS,
  EDGE_METHODS,
  REDIRECT_CODES,
  edgeRuleName,
  edgeRuleProblem,
  edgeRuleSummary,
  edgeRulesState,
  hitsOf,
  ingressLimitRequests,
  moveRule,
  newEdgeRule,
  refusedRuleIndex,
  savedEdgeRule,
} from "@/components/deploy/settings/edge-rules-model"

type EdgeDraft = {
  rateLimit?: DeploymentRateLimit
  edgeRules: DeploymentEdgeRule[]
  crowdSec: boolean
}

type PathKind = "none" | "prefix" | "pattern"

/** The 24 hours the readout counts over, with which proxy wrote them. */
type RequestReading = { driver?: string; summary?: { edgeRules?: RequestFacet[] } }

const DEFAULT_LIMIT: DeploymentRateLimit = { requests: 10, per: "second", burst: 20 }

/**
 * Edge rules — what the proxy decides before the application is asked: a
 * request rate per client address, CrowdSec's bans, and an ordered list of
 * rules whose first match decides. They are the route's, like the address
 * list above them, so one form under the domains rather than a control on
 * each; and they are listed in the order a request meets them.
 *
 * Each rule says how often it decided in the last day, read from the request
 * record, which carries the deciding rule's name. On the Docker Caddy
 * ingress the rate limit is the host's — Caddy has no module for it — and
 * the form says so beside a button that adds it to the network gateway
 * through the gateway's own route.
 */
export function EdgeRulesForm({
  configuration,
  save,
}: {
  configuration: DeploymentEnvironmentConfiguration
  save: ReturnType<typeof useConfiguration>["save"]
}) {
  const { can } = useAuth()
  const canAdmin = can("system.admin")
  const project = useProject()
  const saved: EdgeDraft = {
    rateLimit: configuration.runtime.rateLimit,
    edgeRules: configuration.runtime.edgeRules ?? [],
    crowdSec: Boolean(configuration.runtime.crowdSec),
  }
  const draft = useSettingDraft(`deploy.${project.projectId}.settings.edge-rules`, saved)
  const value = draft.value
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<{ rule: number; message: string }>()
  const [limitRefusal, setLimitRefusal] = useState<string>()
  const reading = usePoll<RequestReading>(
    (signal) => get(`/deploy/${project.projectId}/requests?limit=1`, undefined, signal),
    60_000,
    [project.projectId],
  ).data
  const hits = reading?.summary?.edgeRules
  const caddy = reading?.driver === "docker-caddy"

  const problems = value.edgeRules.map((rule, index) =>
    edgeRuleProblem(
      rule,
      value.edgeRules.filter((_, other) => other !== index).map((other) => other.name),
    ),
  )
  const setRules = (edgeRules: DeploymentEdgeRule[]) => draft.set({ ...value, edgeRules })
  const setRule = (index: number, rule: DeploymentEdgeRule) =>
    setRules(value.edgeRules.map((current, at) => (at === index ? rule : current)))

  const onSave = async () => {
    if (problems.some(Boolean)) return false
    setSaving(true)
    setRefusal(undefined)
    setLimitRefusal(undefined)
    try {
      await save({
        runtime: (latest) => ({
          ...latest.runtime,
          rateLimit: value.rateLimit,
          edgeRules: value.edgeRules.length ? value.edgeRules.map(savedEdgeRule) : undefined,
          crowdSec: value.crowdSec || undefined,
        }),
      })
      return true
    } catch (error) {
      if (error instanceof ApiError && error.field === "runtime.rateLimit") {
        setLimitRefusal(error.message)
      } else if (error instanceof ApiError && error.field === "runtime.edgeRules") {
        setRefusal({
          rule: refusedRuleIndex(error.message, value.edgeRules),
          message: error.message,
        })
      } else {
        notify.error("Could not save the edge rules", error)
      }
      return false
    } finally {
      setSaving(false)
    }
  }

  const applyOnHost = async () => {
    if (!value.rateLimit) return
    try {
      for (const request of ingressLimitRequests(value.rateLimit, project.detail.deployment.name)) {
        await post("/network/protection/limits", request)
      }
      notify.success("The host now limits new connections per address on 443 and 80")
    } catch (error) {
      notify.error("Could not add the host-level limits", error)
    }
  }

  return (
    <SettingForm
      name="Edge rules"
      onSave={onSave}
      dirty={draft.dirty}
      changes={draft.changes}
      saving={saving}
      canEdit={canAdmin}
      onDiscard={() => {
        setRefusal(undefined)
        setLimitRefusal(undefined)
        draft.discard()
      }}
    >
      <SettingSection
        title="Edge rules"
        state={edgeRulesState(value.edgeRules, value.rateLimit, value.crowdSec)}
        status={settingStatus({
          dirty: draft.dirty,
          refused: Boolean(refusal || limitRefusal) || problems.some(Boolean),
        })}
        actions={
          canAdmin && (
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() =>
                setRules([...value.edgeRules, newEdgeRule(value.edgeRules.map((r) => r.name))])
              }
            >
              <Plus className="size-3.5" /> Add rule
            </Button>
          )
        }
      >
        <OptionList>
          <OptionRow
            title="Limit requests per address"
            hint="Past it, an address is answered 429 Too Many Requests with Retry-After."
            checked={Boolean(value.rateLimit)}
            onCheckedChange={(on) =>
              draft.set({
                ...value,
                rateLimit: on ? (saved.rateLimit ?? DEFAULT_LIMIT) : undefined,
              })
            }
            disabled={!canAdmin}
          >
            {value.rateLimit && (
              <RateLimitFields
                limit={value.rateLimit}
                onChange={(rateLimit) => draft.set({ ...value, rateLimit })}
                error={limitRefusal}
                disabled={!canAdmin}
              />
            )}
          </OptionRow>
          <OptionRow
            title="Refuse CrowdSec's bans"
            hint="The addresses CrowdSec's engine on this server has banned are answered 403 here, from a list refreshed every minute. Without CrowdSec installed nothing is refused."
            checked={value.crowdSec}
            onCheckedChange={(crowdSec) => draft.set({ ...value, crowdSec })}
            disabled={!canAdmin}
          />
        </OptionList>
        {value.rateLimit && caddy && (
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
            <FormNote tone="warning">
              This server&apos;s ingress is Caddy, which has no rate-limit module, so the route
              itself limits nothing. The limit can be the host&apos;s instead: new connections per
              address on each of ports 443 and 80, for every domain the ingress serves.
            </FormNote>
            {canAdmin ? (
              <Button type="button" size="xs" variant="outline" onClick={() => void applyOnHost()}>
                Apply on the host
              </Button>
            ) : (
              <Button type="button" size="xs" variant="ghost" asChild>
                <Link href="/network/protection">Protection</Link>
              </Button>
            )}
          </div>
        )}
        {value.edgeRules.length === 0 ? (
          <EmptyNote className="px-0 text-left">
            No rules: every request reaches the application. A rule blocks, answers, redirects or
            lets through the requests it matches, and the first rule a request meets decides.
          </EmptyNote>
        ) : (
          <ol aria-label="Edge rules" className="space-y-3">
            {value.edgeRules.map((rule, index) => (
              <EdgeRuleRow
                key={index}
                index={index}
                count={value.edgeRules.length}
                rule={rule}
                hits={hitsOf(hits, rule.name)}
                readHits={hits !== undefined}
                problem={problems[index] ?? (refusal?.rule === index ? refusal.message : undefined)}
                onChange={(next) => setRule(index, next)}
                onMove={(by) => setRules(moveRule(value.edgeRules, index, by))}
                onRemove={() => setRules(value.edgeRules.filter((_, at) => at !== index))}
                disabled={!canAdmin}
              />
            ))}
          </ol>
        )}
        {refusal && refusal.rule < 0 && <FormNote tone="danger">{refusal.message}</FormNote>}
      </SettingSection>
    </SettingForm>
  )
}

function RateLimitFields({
  limit,
  onChange,
  error,
  disabled,
}: {
  limit: DeploymentRateLimit
  onChange: (limit: DeploymentRateLimit) => void
  error?: string
  disabled?: boolean
}) {
  return (
    <FieldRow columns={3}>
      <Field label="Requests" htmlFor="edge-rate-requests" error={error}>
        <Input
          id="edge-rate-requests"
          type="number"
          min={1}
          max={100000}
          value={limit.requests}
          readOnly={disabled}
          className="font-mono"
          onChange={(event) => onChange({ ...limit, requests: Number(event.target.value) || 0 })}
        />
      </Field>
      <Field label="Per" htmlFor="edge-rate-per">
        <Select
          value={limit.per}
          disabled={disabled}
          onValueChange={(per) => onChange({ ...limit, per: per as DeploymentRateLimit["per"] })}
        >
          <SelectTrigger id="edge-rate-per" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="second">Second</SelectItem>
            <SelectItem value="minute">Minute</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      <Field
        label="Burst"
        htmlFor="edge-rate-burst"
        hint="Requests let through at once above the rate."
      >
        <Input
          id="edge-rate-burst"
          type="number"
          min={0}
          max={100000}
          value={limit.burst ?? 0}
          readOnly={disabled}
          className="font-mono"
          onChange={(event) =>
            onChange({ ...limit, burst: Number(event.target.value) || undefined })
          }
        />
      </Field>
    </FieldRow>
  )
}

/** One rule: its name and what it decided, what it matches, what it does. */
function EdgeRuleRow({
  index,
  count,
  rule,
  hits,
  readHits,
  problem,
  onChange,
  onMove,
  onRemove,
  disabled,
}: {
  index: number
  count: number
  rule: DeploymentEdgeRule
  hits: number
  readHits: boolean
  problem?: string
  onChange: (rule: DeploymentEdgeRule) => void
  onMove: (by: -1 | 1) => void
  onRemove: () => void
  disabled?: boolean
}) {
  const id = `edge-rule-${index}`
  const pathKind: PathKind =
    rule.match.pathRegex !== undefined
      ? "pattern"
      : rule.match.pathPrefix !== undefined
        ? "prefix"
        : "none"
  const setMatch = (match: Partial<DeploymentEdgeRule["match"]>) =>
    onChange({ ...rule, match: { ...rule.match, ...match } })
  const setAction = (action: DeploymentEdgeAction) => onChange({ ...rule, action })
  return (
    <li aria-label={`Rule ${index + 1}: ${rule.name}`}>
      <Well className="space-y-3">
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
          <p className="min-w-0 truncate font-mono text-xs text-muted-foreground">
            <span className="text-foreground">{index + 1}.</span> {edgeRuleSummary(rule)}
          </p>
          <span className="flex shrink-0 items-center gap-1">
            <span className="numeric text-hint text-muted-foreground">
              {readHits ? `${hits} in 24 h` : "—"}
            </span>
            {/* type=button: inside the form, a bare button would submit it. */}
            <IconAction
              type="button"
              label={`Move ${rule.name} up`}
              disabled={disabled || index === 0}
              onClick={() => onMove(-1)}
            >
              <ArrowUp />
            </IconAction>
            <IconAction
              type="button"
              label={`Move ${rule.name} down`}
              disabled={disabled || index === count - 1}
              onClick={() => onMove(1)}
            >
              <ArrowDown />
            </IconAction>
            <IconAction
              type="button"
              label={`Remove ${rule.name}`}
              disabled={disabled}
              onClick={onRemove}
            >
              <Trash />
            </IconAction>
          </span>
        </div>
        <FieldRow columns={2}>
          <Field label="Name" htmlFor={`${id}-name`} error={problem}>
            <Input
              id={`${id}-name`}
              value={rule.name}
              readOnly={disabled}
              className="font-mono"
              onChange={(event) => onChange({ ...rule, name: edgeRuleName(event.target.value) })}
            />
          </Field>
          <Field label="Path" htmlFor={`${id}-path`}>
            <div className="flex min-w-0 gap-2">
              <Select
                value={pathKind}
                disabled={disabled}
                onValueChange={(kind) =>
                  setMatch({
                    pathPrefix: kind === "prefix" ? (rule.match.pathRegex ?? "/") : undefined,
                    pathRegex: kind === "pattern" ? (rule.match.pathPrefix ?? "^/") : undefined,
                  })
                }
              >
                <SelectTrigger aria-label="Path match" className="w-40 shrink-0">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">Any path</SelectItem>
                  <SelectItem value="prefix">Starts with</SelectItem>
                  <SelectItem value="pattern">Pattern</SelectItem>
                </SelectContent>
              </Select>
              {pathKind !== "none" && (
                <Input
                  id={`${id}-path`}
                  value={
                    (pathKind === "prefix" ? rule.match.pathPrefix : rule.match.pathRegex) ?? ""
                  }
                  placeholder={pathKind === "prefix" ? "/admin" : "^/blog/[0-9]+$"}
                  readOnly={disabled}
                  className="min-w-0 font-mono"
                  onChange={(event) =>
                    setMatch(
                      pathKind === "prefix"
                        ? { pathPrefix: event.target.value }
                        : { pathRegex: event.target.value },
                    )
                  }
                />
              )}
            </div>
          </Field>
        </FieldRow>
        <Field label="Methods" htmlFor={`${id}-methods`} hint="None matches every method.">
          <ToggleGroup
            id={`${id}-methods`}
            type="multiple"
            variant="outline"
            size="sm"
            disabled={disabled}
            value={rule.match.methods ?? []}
            onValueChange={(methods) =>
              setMatch({
                methods: methods.length ? (methods as DeploymentEdgeMethod[]) : undefined,
              })
            }
            className="flex-wrap justify-start"
          >
            {EDGE_METHODS.map((method) => (
              <ToggleGroupItem key={method} value={method} className="font-mono text-xs">
                {method}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </Field>
        <FieldRow columns={2}>
          <Field
            label="User agent"
            htmlFor={`${id}-agent`}
            hint="A pattern, matched ignoring case."
          >
            <Input
              id={`${id}-agent`}
              value={rule.match.userAgent ?? ""}
              placeholder="curl|python-requests"
              readOnly={disabled}
              className="font-mono"
              onChange={(event) => setMatch({ userAgent: event.target.value || undefined })}
            />
          </Field>
          <ListField
            id={`${id}-clients`}
            label="From addresses"
            placeholder="203.0.113.7 or 10.0.0.0/8"
            values={rule.match.clientFrom ?? []}
            onChange={(clientFrom) =>
              setMatch({ clientFrom: clientFrom.length ? clientFrom : undefined })
            }
            disabled={disabled}
          />
        </FieldRow>
        <ActionFields id={id} action={rule.action} onChange={setAction} disabled={disabled} />
      </Well>
    </li>
  )
}

function ActionFields({
  id,
  action,
  onChange,
  disabled,
}: {
  id: string
  action: DeploymentEdgeAction
  onChange: (action: DeploymentEdgeAction) => void
  disabled?: boolean
}) {
  const choose = (kind: DeploymentEdgeAction["kind"]) => {
    switch (kind) {
      case "status":
        return onChange({ kind, status: 410 })
      case "redirect":
        return onChange({ kind, location: "/", code: 302 })
      case "allow":
        return onChange({ kind })
      default:
        return onChange({ kind: "block", status: 403 })
    }
  }
  return (
    <FieldRow columns={action.kind === "redirect" ? 3 : 2}>
      <Field
        label="Then"
        htmlFor={`${id}-action`}
        hint={
          action.kind === "allow"
            ? "Lets the request through without asking the rules after this one."
            : undefined
        }
      >
        <Select
          value={action.kind}
          disabled={disabled}
          onValueChange={(kind) => choose(kind as DeploymentEdgeAction["kind"])}
        >
          <SelectTrigger id={`${id}-action`} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {EDGE_ACTIONS.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      {action.kind === "status" && (
        <Field label="Status" htmlFor={`${id}-status`}>
          <Input
            id={`${id}-status`}
            type="number"
            min={200}
            max={599}
            value={action.status}
            readOnly={disabled}
            className="font-mono"
            onChange={(event) => onChange({ ...action, status: Number(event.target.value) || 0 })}
          />
        </Field>
      )}
      {action.kind === "redirect" && (
        <>
          <Field label="To" htmlFor={`${id}-location`}>
            <Input
              id={`${id}-location`}
              value={action.location}
              placeholder="https://example.com/ or /new"
              readOnly={disabled}
              className="font-mono"
              onChange={(event) => onChange({ ...action, location: event.target.value })}
            />
          </Field>
          <Field label="Code" htmlFor={`${id}-code`}>
            <Select
              value={String(action.code)}
              disabled={disabled}
              onValueChange={(code) =>
                onChange({ ...action, code: Number(code) as (typeof REDIRECT_CODES)[number] })
              }
            >
              <SelectTrigger id={`${id}-code`} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {REDIRECT_CODES.map((code) => (
                  <SelectItem key={code} value={String(code)}>
                    {code}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </>
      )}
    </FieldRow>
  )
}
