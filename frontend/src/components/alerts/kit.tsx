"use client"

import { useId } from "react"
import Link from "next/link"
import { Plus } from "@/components/icons"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { NotificationChannel } from "@/lib/types"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { Field, FormNote, OptionList, OptionRow } from "@/components/form"
import { ProductGlyph, ProductLogo, hasProductLogo } from "@/components/product-logo"
import { ChannelGlyph } from "@/components/deploy/vocabulary"
import { Bone } from "@/components/bone"
import { WireHost, WireMark, WirePlaceholder } from "@/components/wire/wire"
import { WireFlow, type WireFlowNode } from "@/components/wire/wire-flow"
import { SidePanel, SidePanelFooter } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import {
  channelNode,
  isFiring,
  pictureTargets,
  type AlertSubjectView,
  type AlertTone,
} from "@/components/alerts/alert-parts"
import {
  NOT_HERE,
  alertCounts,
  areaProduct,
  judging,
  kindProduct,
  presentKinds,
  reaches,
  type AlertArea,
  type AlertKinds,
  type AlertRuleView,
} from "@/components/alerts/kinds"

/**
 * The parts of an alerts page that know there are kinds of rule, generic over
 * the page's own union of kinds: the tile a rule is drawn as, the card that
 * adds one, the picture of what the rules watch and the sheet a rule is edited
 * in. The proxy's and the host's Alerts pages each carried a copy of all five,
 * which differed only in the kind type and how a kind found its product, and
 * had drifted (only the host's picture kept its shape while the rules loaded).
 * Each page now keeps its table (`AlertKinds`) and its API.
 */

/**
 * A section head on an alerts page. Firing carries a verb and Recently told
 * beside it carries none, so a plain head that took its height from what was
 * in it drew the two titles 5px apart and their hairlines 10px apart across
 * the gutter. Held to the height of a row of verbs, heads side by side line
 * up whatever each one carries (PGP-09).
 */
export const PAIRED_HEAD = "min-h-8"

/**
 * A rule's tile: what it watches, drawn as that product where it is one, with
 * the kind's own glyph in the corner (Let's Encrypt with a clock is a
 * certificate running low, Docker with a stop a container gone down), and as
 * the kind's glyph alone where it is the machine itself.
 */
export function KindTile<K extends string>({
  kinds,
  kind,
  engine,
  tone,
  size = "md",
  className,
}: {
  kinds: AlertKinds<K>
  kind: K
  engine?: string
  tone?: AlertTone
  size?: "sm" | "md"
  className?: string
}) {
  const product = kindProduct(kinds, kind, engine)
  const Glyph = kinds.kinds[kind].icon
  if (!hasProductLogo(product))
    return (
      <ProductLogo
        size={size}
        fallback={Glyph}
        className={cn(
          tone === "danger" && "border-rule-danger [&_svg]:text-destructive",
          tone === "warning" && "border-rule-warning [&_svg]:text-warning",
          className,
        )}
      />
    )
  return (
    <span className={cn("relative flex shrink-0", className)}>
      <ProductLogo
        id={product}
        size={size}
        className={cn(
          tone === "danger" && "border-rule-danger",
          tone === "warning" && "border-rule-warning",
        )}
      />
      <span
        aria-hidden
        className={cn(
          "absolute -right-1 -bottom-1 flex size-4.5 items-center justify-center rounded-md border bg-background",
          tone === "danger"
            ? "border-rule-danger text-destructive"
            : tone === "warning"
              ? "border-rule-warning text-warning"
              : "border-hairline text-muted-foreground",
        )}
      >
        <Glyph className="size-3" />
      </span>
    </span>
  )
}

/**
 * A rule that could be added, where there is none yet: what it watches and
 * the line it would be added with, the sentence behind it a hover away. The
 * form, where the kind is chosen with room, says the rest.
 */
export function KindCard<K extends string>({
  kinds,
  kind,
  engine,
  absent,
  onClick,
}: {
  kinds: AlertKinds<K>
  kind: K
  engine?: string
  /** Nothing on this server for the kind to read: said in its line, and not offered. */
  absent?: boolean
  /** Left out, the card is drawn but cannot be pressed: an account that only reads. */
  onClick?: () => void
}) {
  const spec = kinds.kinds[kind]
  const press = absent ? undefined : onClick
  return (
    <ChoiceCard
      onClick={press}
      disabled={!press}
      className="min-h-0 flex-row items-center gap-3 px-3 py-2.5"
    >
      <KindTile kinds={kinds} kind={kind} engine={engine} size="sm" />
      {/* The tooltip is the words' own: a ChoiceCard keeps `title` for its name. */}
      <span className="flex min-w-0 flex-1 flex-col" title={spec.detail ?? spec.hint}>
        <ChoiceCardTitle className="truncate">{spec.label}</ChoiceCardTitle>
        <ChoiceCardHint className="truncate leading-snug">
          {absent ? NOT_HERE : (spec.fact ?? spec.hint)}
        </ChoiceCardHint>
      </span>
      {press && <Plus aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />}
    </ChoiceCard>
  )
}

/**
 * The cards with nothing added yet, each a press away from its rule. A
 * paragraph stood over them saying what a rule is; the picture and the line
 * above say it.
 */
export function KindCards<K extends string>({
  kinds,
  engine,
  absent,
  onAdd,
}: {
  kinds: AlertKinds<K>
  engine?: string
  /** The kinds that judge nothing on this server. */
  absent?: ReadonlySet<string>
  onAdd?: (kind: K) => void
}) {
  return (
    <ChoiceGrid
      columns={2}
      role="group"
      aria-label="Rules to add"
      className="grid-cols-1 sm:grid-cols-2 lg:grid-cols-2"
    >
      {kinds.order.map((kind) => (
        <KindCard
          key={kind}
          kinds={kinds}
          kind={kind}
          engine={engine}
          absent={absent?.has(kind)}
          onClick={onAdd ? () => onAdd(kind) : undefined}
        />
      ))}
    </ChoiceGrid>
  )
}

/**
 * What the rules watch, wired through this server to who is told: the
 * Notifications picture's shape, read left to right, and since 0.9.1 the same
 * `WireFlow` that draws it. A watched area is drawn as its product; its wire
 * is red while something there fires, amber while one holds, moving where a
 * rule watches it and dashed where none does, and pressing an unwatched one
 * opens a rule for it. A channel's wire moves while an enabled rule reaches
 * it; a paused one is still and grey.
 *
 * It was laid by hand as one column a side, every area a row with its words
 * beside it: the host's eight sources stood 480px tall in the middle third of
 * the page. As a `WireFlow` of small marks they are rows on level lines, two
 * rows of four on a wide page.
 *
 * While `waiting` (the rules not read yet) every area keeps its place without
 * its words, so nothing under the picture moves when they land.
 *
 * On a phone it takes the phone form of `pictures-on-a-phone.md` its page
 * calls for. `marks` (the default): the areas' and the channels' marks with
 * the server named between them, for a page whose kind cards and rules name
 * the areas again. `stack`: the areas, the server and the channels stacked
 * with their words and no lines, for a page where the picture is the only
 * place an area that is not watched is named — the host's eight sources,
 * whose marks ran off as a row of seven with no captions.
 */
export function AlertsPicture<K extends string>({
  kinds,
  rules,
  subjects,
  channels,
  engine,
  title,
  reach,
  waiting,
  absent,
  onAdd,
  phone = "marks",
}: {
  kinds: AlertKinds<K>
  rules: AlertRuleView<K>[]
  subjects: (AlertSubjectView & { kind: K })[]
  channels: NotificationChannel[]
  engine?: string
  /** What the server in the middle says: how often it checks, or how many rules are on. */
  title: React.ReactNode
  /** Its second line: what it does with a change, or who it tells. */
  reach: React.ReactNode
  waiting?: boolean
  /**
   * The kinds that judge nothing on this server. An area with no other kind
   * says so in place of "not watched" and cannot be pressed; a rule of such a
   * kind is not a rule watching its area.
   */
  absent?: ReadonlySet<string>
  onAdd?: (kind: K) => void
  /** The phone's form: the marks alone, or every word stacked. */
  phone?: "marks" | "stack"
}) {
  const enabled = judging(rules, absent)
  return (
    <WireFlow
      phone={phone}
      size="sm"
      start={{
        title: "What the rules watch",
        nodes: kinds.areas.map((area) =>
          areaNode({
            area,
            engine,
            rules: enabled.filter((rule) => area.kinds.includes(rule.kind)),
            subjects: subjects.filter((subject) => area.kinds.includes(subject.kind)),
            waiting,
            present: presentKinds(area, absent),
            onAdd,
          }),
        ),
      }}
      hub={{
        mark: (size) => <WireHost size={size} />,
        eyebrow: "This server",
        title: waiting ? <Bone className="w-16" /> : title,
        hint: waiting ? <Bone className="w-24" /> : reach,
      }}
      end={{
        title: "Who is told",
        nodes: pictureTargets(channels).map((target) =>
          channelNode(target, target.channel ? reaches(rules, target.channel) : false),
        ),
      }}
    />
  )
}

/** One watched area as the picture's node, with the wire its state draws. */
function areaNode<K extends string>({
  area,
  engine,
  rules,
  subjects,
  waiting,
  present,
  onAdd,
}: {
  area: AlertArea<K>
  engine?: string
  /** The enabled rules that watch it. */
  rules: AlertRuleView<K>[]
  subjects: AlertSubjectView[]
  waiting?: boolean
  /** Its kinds with something to read on this server; none, and the area is not here. */
  present: K[]
  onAdd?: (kind: K) => void
}): WireFlowNode {
  const product = areaProduct(area, engine)
  const Glyph = area.fallback
  const { firing, holding } = alertCounts(subjects)
  const watched = rules.length > 0
  const tone = firing > 0 ? "danger" : holding > 0 ? "warning" : undefined
  const here = present.length > 0
  const words = !here
    ? NOT_HERE
    : !watched
      ? "not watched"
      : firing > 0
        ? `${firing} firing`
        : holding > 0
          ? `${holding} holding`
          : `${plural(rules.length, "rule")}, all quiet`
  return {
    id: `area-${area.key}`,
    name: `${area.title}: ${words}`,
    label: watched || !here ? undefined : `Watch ${area.title.toLowerCase()}`,
    placeholder: !watched,
    onPress: here && !watched && !waiting && onAdd ? () => onAdd(present[0]) : undefined,
    mark: (size) =>
      watched ? (
        <WireMark tone={tone ?? "logo"} size={size}>
          {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Glyph aria-hidden />}
        </WireMark>
      ) : (
        <WirePlaceholder size={size} product={product} fallback={Glyph} />
      ),
    title: watched ? area.title : <span className="text-muted-foreground">{area.title}</span>,
    hint: waiting ? (
      <Bone className="w-16" />
    ) : (
      <span
        className={cn(
          tone === "danger" && "font-medium text-destructive",
          tone === "warning" && "font-medium text-warning",
        )}
      >
        {words}
      </span>
    ),
    line: !watched
      ? { state: "idle" }
      : tone
        ? { state: tone }
        : { state: "up", moving: true, duration: 2.4 },
  }
}

/**
 * The sheet a rule is added or edited in, over the page, so the page behind
 * it stays as it was. Its body is the page's own form, drawn with `RuleForm`.
 */
export function RuleSheet({
  open,
  editing,
  onClose,
  children,
}: {
  open: boolean
  /** An existing rule is being edited, rather than one added. */
  editing: boolean
  onClose: () => void
  /** The form, mounted while a rule is open and keyed so a new one starts clean. */
  children?: React.ReactNode
}) {
  return (
    <SidePanel
      open={open}
      onOpenChange={(next) => !next && onClose()}
      title={editing ? "Edit alert" : "Add alert"}
      description="What to watch, and who is told when it starts and when it stops."
      size="lg"
    >
      {children}
    </SidePanel>
  )
}

/**
 * One rule as a form: what to watch as one choice of the page's kinds, the
 * kind's own fields (`children`), who to tell, and a footer that says the
 * rule back before it is saved. The page holds the fields' state and the
 * save; this holds the shape every alerts page shares. It brings its own
 * footer, so the command stays beside the rule it saves.
 */
export function RuleForm<K extends string>({
  editing,
  onCancel,
  kinds,
  kind,
  onKind,
  absent,
  engine,
  channels,
  selected,
  onSelected,
  sentence,
  saving,
  onSave,
  children,
}: {
  /** An existing rule is being edited, rather than one added. */
  editing: boolean
  onCancel: () => void
  kinds: AlertKinds<K>
  kind: K
  onKind: (kind: K) => void
  /** The kinds that judge nothing on this server: named, and not to be chosen. */
  absent?: ReadonlySet<string>
  engine?: string
  channels: NotificationChannel[]
  selected: number[]
  onSelected: (next: number[]) => void
  /** The rule said back: "CPU high: 90% or more over 10 min". */
  sentence: React.ReactNode
  saving: boolean
  onSave: () => void
  /** The kind's own fields, between the kinds and the channels. */
  children?: React.ReactNode
}) {
  const formId = useId()
  return (
    <form
      id={formId}
      className="flex flex-col gap-6"
      onSubmit={(event) => {
        event.preventDefault()
        onSave()
      }}
    >
      <Field label="Watch for">
        <ChoiceGrid
          columns={2}
          mode="single"
          label="Alert kind"
          className="grid-cols-1 sm:grid-cols-2 lg:grid-cols-2"
        >
          {kinds.order.map((option) => (
            <ChoiceCard
              key={option}
              selected={kind === option}
              // The kind of the rule being edited stays chosen; any other is not offered.
              disabled={absent?.has(option) && kind !== option}
              onClick={() => onKind(option)}
              className="min-h-0 flex-row gap-3 p-3"
            >
              <KindTile kinds={kinds} kind={option} engine={engine} size="sm" />
              <span className="min-w-0 space-y-0.5" title={kinds.kinds[option].detail}>
                <ChoiceCardTitle className="block truncate">
                  {kinds.kinds[option].label}
                </ChoiceCardTitle>
                <ChoiceCardHint className="line-clamp-2">
                  {absent?.has(option) ? NOT_HERE : kinds.kinds[option].hint}
                </ChoiceCardHint>
              </span>
            </ChoiceCard>
          ))}
        </ChoiceGrid>
      </Field>

      {children}

      <Field label="Tell">
        {channels.length === 0 ? (
          <FormNote>
            No notification channels yet — the rule reaches every channel you add later.{" "}
            <Link
              href="/deploy/notifications"
              className="text-foreground underline-offset-2 hover:underline"
            >
              Add a channel
            </Link>
          </FormNote>
        ) : (
          <div className="space-y-1.5">
            <OptionList>
              {channels.map((channel) => (
                <OptionRow
                  key={channel.id}
                  title={
                    <span className="inline-flex items-center gap-2">
                      <ChannelGlyph channel={channel} />
                      {channel.name}
                    </span>
                  }
                  hint={`${channel.target || channel.url}${channel.enabled ? "" : " · paused"}`}
                  checked={selected.includes(channel.id)}
                  onCheckedChange={(on) =>
                    onSelected(
                      on ? [...selected, channel.id] : selected.filter((id) => id !== channel.id),
                    )
                  }
                />
              ))}
            </OptionList>
            <FormNote>
              {selected.length === 0
                ? "None chosen: every enabled channel is told."
                : `${selected.length} chosen.`}
            </FormNote>
          </div>
        )}
      </Field>

      <SidePanelFooter>
        <p className="mr-auto min-w-0 basis-full text-hint text-muted-foreground sm:basis-auto">
          {sentence}
        </p>
        <Button type="button" variant="outline" onClick={onCancel} disabled={saving}>
          Cancel
        </Button>
        <Button type="submit" form={formId} pending={saving}>
          {editing ? "Save alert" : "Add alert"}
        </Button>
      </SidePanelFooter>
    </form>
  )
}

export { isFiring }
