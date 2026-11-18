import { classes, elementName, elementOf, literalProp } from "./design-system.mjs"

/**
 * The components and habits the 0.9.0 UI-system pass retired, as an ESLint
 * plugin.
 *
 * `design-system.mjs` refuses a value typed by hand where a token exists. This
 * refuses the other half: a pattern assembled by hand where a component owns
 * it, and a name that was kept only so its callers could move. Each of these
 * had a count when the pass began (341 `FactDot`s, 75 files of `tabClasses`,
 * 55 raw `ToggleGroup`s, 17 hand-built menus, three `CopyButton`s), and each
 * count came from the same place: the shared component existed, and nothing
 * said so at the moment the copy was typed.
 *
 * An id has homes, the files that are the component and so are allowed its
 * parts. A message names the replacement and the page of
 * `docs/internal/frontend/components/` that describes it.
 */

const src = (pattern) => new RegExp(`src[\\\\/]${pattern}`)
const UI = src("components[\\\\/]ui[\\\\/]")

/**
 * The glyphs a "more" menu opens from, by the name they are imported under
 * (`components/icons.tsx` calls it `More`; the icon sets call it the rest).
 */
const MORE_GLYPH =
  /^(?:More|MoreHorizontal|MoreVertical|Ellipsis|EllipsisHorizontal|EllipsisVertical|Dots|DotsHorizontal|DotsVertical)(?:Icon)?$/

/** The local names a hand-drawn copy control goes by. */
const COPY_CONTROL = /^Copy(?:Button|Line|Path|List|Chip)$/

const RULES = {
  "fact-dot": {
    homes: [
      src("components[\\\\/]fact-line\\.tsx$"),
      src("components[\\\\/]identity-header\\.tsx$"),
    ],
    message:
      "FactDot is retired: a dot drawn as its own item is left at the end of a line when the next fact wraps. Write the line as a FactLine of Fact children (components/fact-line.tsx), or pass an identity header's `facts` as Facts; HostFact is `Fact product` (components/values-facts.md).",
  },
  "host-identity": {
    homes: [src("components[\\\\/]identity-header\\.tsx$")],
    message:
      "HostIdentity is the old name of IdentityHeader. Import IdentityHeader from components/identity-header (components/page-header.md).",
  },
  "tab-strip": {
    // The editor tabs keep the look for a different control: tabs that are
    // documents, opened, closed and reordered.
    homes: [
      src("components[\\\\/]tabs\\.tsx$"),
      src("components[\\\\/]ui[\\\\/]tabs\\.tsx$"),
      src("components[\\\\/]database[\\\\/]query[\\\\/]tab-strip\\.tsx$"),
    ],
    message:
      "A view strip assembled by hand. tabClasses and a raw TabsPrimitive.List gave 75 files four keyboard and ARIA models for one mark. Use ViewTabs (or ViewTabList inside a Tabs you hold), and ViewLinks for a strip that changes route, from components/tabs.tsx (components/tabs-chips-segments.md).",
  },
  "toggle-group": {
    homes: [src("components[\\\\/]segments\\.tsx$"), UI],
    message:
      'A raw single ToggleGroup. Use Segments (components/segments.tsx): it requires a label, takes its name from a Field, and ignores the empty value a second press sends. `type="multiple"` stays a ToggleGroup (components/tabs-chips-segments.md).',
  },
  "more-menu": {
    homes: [src("components[\\\\/]verbs\\.tsx$"), UI],
    message:
      "A “more” menu assembled by hand. Describe the verbs and hand them to VerbMenu, VerbActions or VerbBar (components/verbs.tsx): they draw the trigger, put a destructive verb last in its own ink and make a verb with an `href` a link (components/menus.md).",
  },
  clipboard: {
    homes: [src("lib[\\\\/]clipboard\\.ts$"), src("components[\\\\/]copy\\.tsx$")],
    message:
      "The clipboard is reached through lib/clipboard.ts (copyText, copyTextQuietly, copyPending, pasteText, canPaste), which checks that there is one and says the one sentence when there is not. A copy control is CopyButton, CopyAction or CopyValue from components/copy.tsx, and useCopy (hooks/use-copy) gives a tick of its own (components/values.md).",
  },
  "copy-control": {
    homes: [src("components[\\\\/]copy\\.tsx$")],
    message:
      "A copy control declared beside its caller. Use CopyButton, CopyAction or CopyValue from components/copy.tsx, and CodeLine or CodeBlock from components/code.tsx for a value with its copy (components/values.md).",
  },
  "list-skeleton": {
    homes: [src("components[\\\\/]choice-row\\.tsx$")],
    message:
      "A list of cards imitated while it loads. Use ChoiceListSkeleton (components/choice-row.tsx), which is a ChoiceRow's height, in place of GhostRows, CardRowsSkeleton, CardsSkeleton, a LoadingRows reshaped with `[&>*]:rounded-xl`, or a Skeleton typed at a row's height (components/card-row.md).",
  },
  sweep: {
    homes: [src("components[\\\\/]loading\\.tsx$")],
    message:
      "A sweep bar drawn by hand. Use Sweep (components/loading.tsx): one fill, one track, and one answer to reduced motion and to “Pause animations” (components/states.md).",
  },
  "status-dot": {
    homes: [src("components[\\\\/]status-dot\\.tsx$"), UI],
    message:
      "A status dot typed by hand. Use StatusDot (components/status-dot.tsx): `tone` for the colour, `live` for one that breathes, `corner` with a label for the dot on a mark, so the state is said and not only coloured (components/status-tag.md).",
  },
  "stat-grid": {
    // The three headline sets §15 pass 2 keeps, as stat-tile.tsx's own doc lists them.
    homes: [
      src("components[\\\\/]stat-tile\\.tsx$"),
      src("app[\\\\/]\\(dashboard\\)[\\\\/]metrics[\\\\/]page\\.tsx$"),
      src("components[\\\\/]docker[\\\\/]"),
      src("components[\\\\/]logs[\\\\/]insights\\.tsx$"),
      // A project's audience is a log lens on its requests, with StatButtons.
      src("components[\\\\/]deploy[\\\\/]audience\\.tsx$"),
    ],
    message:
      "A row of tiles. A reading is said in words: a ReadingLine (components/reading-line.tsx), a fact on the identity line or a node of the picture. StatGrid stays on Metrics, the Docker overview and a log lens, as components/stat-tile.tsx lists (components/readings.md).",
  },
  "icon-button": {
    homes: [src("components[\\\\/]icon-action\\.tsx$"), UI],
    message:
      "An icon-only Button. Use IconAction (components/icon-action.tsx): its `label` is required and is both the accessible name and the tooltip. A Button that is a trigger's child (`asChild`) or draws something of its own stays a Button (components/menus.md).",
  },
  "back-link": {
    homes: [],
    message:
      "A back link built as a constant. Pass `back={{ href, label }}` to PageContext, IdentityHeader or DetailPage (components/page.tsx), which draw the one BackLink (components/page-header.md).",
  },
  "route-params": {
    homes: [src("lib[\\\\/]route-params\\.ts$")],
    message:
      "A route segment decoded by hand. decodeURIComponent throws on a stray “%” and takes the page to the error screen; read the segment with decodeSegment from lib/route-params.ts (components/values.md).",
  },
}

/** Whether a JSX subtree draws an element of one of these names. */
function draws(node, test) {
  if (!node || typeof node !== "object") return false
  if (node.type === "JSXOpeningElement" && test(elementName(node) ?? "")) return true
  for (const key of Object.keys(node)) {
    if (key === "parent") continue
    const child = node[key]
    if (Array.isArray(child)) {
      if (child.some((item) => draws(item, test))) return true
    } else if (child && typeof child.type === "string" && draws(child, test)) return true
  }
  return false
}

const hasProp = (element, prop) =>
  element.attributes.some((a) => a.type === "JSXAttribute" && a.name?.name === prop)

const rule = {
  meta: {
    type: "problem",
    docs: { description: "Use the shared component, not the pattern or the name it retired." },
    // `off` names the ids a file is not held to yet. eslint.config.mjs lists
    // each such file with its reason, and the list only shrinks.
    schema: [
      {
        type: "object",
        properties: { off: { type: "array", items: { type: "string" } } },
        additionalProperties: false,
      },
    ],
  },
  create(context) {
    const filename = context.filename ?? ""
    const off = new Set(context.options[0]?.off ?? [])
    const on = (id) => !off.has(id) && !RULES[id].homes.some((home) => home.test(filename))
    const report = (id, node) => {
      if (on(id)) context.report({ node, message: `[${id}] ${RULES[id].message}` })
    }
    const page = /\.tsx$/.test(filename) || src("app[\\\\/]").test(filename)

    const classList = (node, text) => {
      if (typeof text !== "string" || text.length === 0) return
      const list = classes(text).map((c) => c.utility)
      if (list.includes("animate-sweep")) report("sweep", node)
      // The 6px dot: round, that size, and a ground. A dot that is a drawing
      // (a browser's window buttons) says so on its own disable line.
      if (
        list.includes("rounded-full") &&
        list.includes("size-1.5") &&
        list.some((c) => /^bg-/.test(c))
      )
        report("status-dot", node)
      // LoadingRows forced into card shape.
      if (classes(text).some((c) => c.utility === "rounded-xl" && c.variants.includes("[&>*]")))
        report("list-skeleton", node)
      // A Skeleton at a ChoiceRow's height and radius is a ChoiceRow's.
      const element = elementOf(node)
      if (
        elementName(element) === "Skeleton" &&
        list.includes("rounded-xl") &&
        list.some((c) => /^(?:min-)?h-(?:14|\[3\.5rem\]|\[3\.625rem\]|\[56px\]|\[58px\])$/.test(c))
      )
        report("list-skeleton", node)
    }

    return {
      ImportDeclaration(node) {
        for (const specifier of node.specifiers) {
          if (specifier.type !== "ImportSpecifier") continue
          const name = specifier.imported.name ?? specifier.imported.value
          const local = specifier.local.name
          if (name === "FactDot" || name === "HostFact") report("fact-dot", specifier)
          if (name === "HostIdentity" || local === "HostIdentity")
            report("host-identity", specifier)
          if (name === "tabClasses") report("tab-strip", specifier)
          if (name === "GhostRows" || name === "CardRowsSkeleton" || name === "CardsSkeleton")
            report("list-skeleton", specifier)
          if (name === "StatGrid") report("stat-grid", specifier)
        }
      },
      // A retired name declared again is the copy coming back.
      "FunctionDeclaration, VariableDeclarator"(node) {
        const name = node.id?.type === "Identifier" ? node.id.name : undefined
        if (!name) return
        if (name === "GhostRows" || name === "CardRowsSkeleton" || name === "CardsSkeleton")
          report("list-skeleton", node.id)
        if (COPY_CONTROL.test(name)) report("copy-control", node.id)
        if (
          name === "BACK" &&
          node.type === "VariableDeclarator" &&
          (node.init?.type === "JSXElement" || node.init?.type === "JSXFragment")
        )
          report("back-link", node.id)
      },
      JSXOpeningElement(node) {
        const name = elementName(node)
        if (node.name.type === "JSXMemberExpression") {
          if (node.name.object.name === "TabsPrimitive" && name === "List")
            report("tab-strip", node)
          return
        }
        if (name === "ToggleGroup" && literalProp(node, "type") !== "multiple")
          report("toggle-group", node)
        if (name === "DropdownMenuTrigger" && draws(node.parent, (n) => MORE_GLYPH.test(n)))
          report("more-menu", node)
        if (
          name === "Button" &&
          /^icon(?:-|$)/.test(String(literalProp(node, "size") ?? "")) &&
          !hasProp(node, "asChild") &&
          !asChildOf(node)
        )
          report("icon-button", node)
      },
      MemberExpression(node) {
        if (
          node.object.type === "Identifier" &&
          node.object.name === "navigator" &&
          node.property.type === "Identifier" &&
          node.property.name === "clipboard" &&
          !node.computed
        )
          report("clipboard", node)
      },
      CallExpression(node) {
        if (node.callee.type !== "Identifier") return
        if (node.callee.name === "tabClasses") report("tab-strip", node)
        // In a page or a component, the only thing there is to decode is the
        // address. A model (`.ts`) decodes other things: a DSN, a hash, a key.
        if (node.callee.name === "decodeURIComponent" && page) report("route-params", node)
      },
      Literal(node) {
        classList(node, node.value)
      },
      TemplateElement(node) {
        classList(node, node.value?.cooked)
      },
    }
  },
}

/**
 * Whether a Button is the child a trigger was given with `asChild`
 * (`<DropdownMenuTrigger asChild><Button size="icon" …`), or handed to a
 * component as a prop (`trigger={<Button …/>}`), which then makes it a
 * trigger's child: the trigger is the control, and the Button only its face.
 */
function asChildOf(node) {
  // Out through the expression it is written in (`{trigger ?? <Button …/>}`).
  let at = node.parent?.parent
  while (
    at &&
    (at.type === "JSXExpressionContainer" ||
      at.type === "LogicalExpression" ||
      at.type === "ConditionalExpression")
  ) {
    if (at.type === "JSXExpressionContainer" && at.parent?.type === "JSXAttribute") return true
    at = at.parent
  }
  if (at?.type !== "JSXElement" || !hasProp(at.openingElement, "asChild")) return false
  // A tooltip around an icon Button is what IconAction is.
  return elementName(at.openingElement) !== "TooltipTrigger"
}

const plugin = { rules: { retired: rule } }

export default plugin
export const RETIRED_IDS = Object.keys(RULES)
