import { defineConfig, globalIgnores } from "eslint/config"
import nextVitals from "eslint-config-next/core-web-vitals"
import nextTs from "eslint-config-next/typescript"
import jsxA11y from "eslint-plugin-jsx-a11y"

import designSystem from "./eslint-rules/design-system.mjs"
import retiredApis, { RETIRED_IDS } from "./eslint-rules/retired-apis.mjs"

/*
 * The files a guardrail id does not hold yet, each group with its reason. Both local rules are on
 * everywhere else, and on every other id here; a file leaves this list when its page moves onto
 * the shared component, and nothing joins it (0.9.0, WP-39). A file in several groups is excused
 * from the ids of each.
 */
const NOT_YET = [
  {
    why: "A file WP-45 owns in wave 5, which moves it onto the shared components on its own branch.",
    files: {
      "src/app/(dashboard)/system-users/page.tsx": [
        "animate-pulse-status",
        "fact-dot",
        "host-identity",
      ],
      "src/components/bar-reading.tsx": ["sweep"],
      "src/components/database/generate/index.tsx": ["list-skeleton"],
      "src/components/database/shell/database-header.tsx": ["host-identity"],
      "src/components/database/workbench/chrome.tsx": ["container-arbitrary"],
      "src/components/deploy/build-console.tsx": ["sweep"],
      "src/components/deploy/settings/domains.tsx": ["container-arbitrary"],
      "src/components/map/nodes.tsx": ["radius-arbitrary", "status-dot"],
      "src/components/procs/cron-job-parts.tsx": ["stat-grid"],
      "src/components/procs/cron-jobs.tsx": ["status-dot", "toggle-group"],
      "src/components/procs/pm2-app-page.tsx": ["fact-dot"],
      "src/components/procs/pm2-scale-dialog.tsx": ["icon-button"],
      "src/components/procs/process-detail.tsx": ["stat-grid"],
      "src/components/procs/process-table.tsx": ["fact-dot", "host-identity", "media-literal"],
      "src/components/procs/schedule-band.tsx": ["animate-pulse-status", "fact-dot"],
      "src/components/procs/service-views.tsx": ["stat-grid"],
      "src/components/procs/services.tsx": ["fact-dot", "host-identity"],
      "src/components/procs/timer-parts.tsx": ["stat-grid"],
      "src/components/shell/app-sidebar.tsx": ["status-dot"],
    },
  },
  {
    why: "A view strip still drawn with tabClasses: moving it onto ViewTabs changes the roles and keys its page's specs read, so each moves with its page (A11Y-02).",
    ids: ["tab-strip"],
    files: [
      "src/app/(dashboard)/network/protection/page.tsx",
      "src/app/(dashboard)/packages/page.tsx",
      "src/components/assistant/assistant-page.tsx",
      "src/components/database/connect/index.tsx",
      "src/components/database/mongo/aggregations/pipeline-view.tsx",
      "src/components/database/mongo/documents/index.tsx",
      "src/components/database/mongo/explain.tsx",
      "src/components/database/ops/access-grants.tsx",
      "src/components/database/ops/access-mongo.tsx",
      "src/components/database/ops/performance.tsx",
      "src/components/database/redis/console/index.tsx",
      "src/components/database/redis/keys/json-editor.tsx",
      "src/components/database/redis/keys/stream-editor.tsx",
      "src/components/database/redis/keys/string-editor.tsx",
      "src/components/deploy/new-project.tsx",
      "src/components/deploy/project-logs.tsx",
      "src/components/docker/network-page.tsx",
      "src/components/files/quick-open.tsx",
      "src/components/git/clone-dialog.tsx",
      "src/components/git/conflict-preview.tsx",
      "src/components/logs/log-workspace.tsx",
      "src/components/logs/request-records.tsx",
      "src/components/proxy/certs-panel.tsx",
      "src/components/proxy/config-editor.tsx",
      "src/components/proxy/request-tester.tsx",
      "src/components/proxy/site-logs.tsx",
      "src/components/security/intrusion-panels.tsx",
      "src/components/shell/command-palette.tsx",
      "src/components/terminal/workspace-tools.tsx",
    ],
  },
  {
    why: "A raw single ToggleGroup still to become Segments (PRM-07).",
    ids: ["toggle-group"],
    files: [
      "src/app/(dashboard)/files/page.tsx",
      "src/app/(dashboard)/metrics/page.tsx",
      "src/components/account/api-keys.tsx",
      "src/components/database/fleet/mcp-server-dialog.tsx",
      "src/components/database/home/activity.tsx",
      "src/components/deploy/environment-switch.tsx",
      "src/components/deploy/project-database.tsx",
      "src/components/deploy/project-runtime.tsx",
      "src/components/deploy/settings/variable-type.tsx",
      "src/components/deploy/site-preview.tsx",
      "src/components/docker/container-shell-tab.tsx",
      "src/components/files/quick-open.tsx",
      "src/components/logs/filter-bar.tsx",
      "src/components/metrics/hardware-panels.tsx",
      "src/components/metrics/range-picker.tsx",
      "src/components/network/connections/address-rail.tsx",
      "src/components/network/connections/block-dialog.tsx",
      "src/components/network/traffic/interface-charts.tsx",
      "src/components/network/vpn/record.tsx",
      "src/components/proxy/access-rules.tsx",
      "src/components/proxy/alerts-page.tsx",
      "src/components/proxy/cert-detail.tsx",
      "src/components/proxy/certbot-panel.tsx",
      "src/components/proxy/config-history.tsx",
      "src/components/proxy/ports-proxy.tsx",
      "src/components/proxy/private-cert-dialog.tsx",
      "src/components/proxy/site-form.tsx",
      // Cut out of site-form.tsx with their raw ToggleGroups (CQ-01); they leave the list with it.
      "src/components/proxy/site-form/access.tsx",
      "src/components/proxy/site-form/limits.tsx",
      "src/components/proxy/site-form/upstream.tsx",
      "src/components/proxy/stream-servers.tsx",
      "src/components/proxy/stream-setup.tsx",
    ],
  },
  {
    why: "A hand-built ⋯ menu still to become VerbMenu (PRM-22).",
    ids: ["more-menu"],
    files: [
      "src/app/(dashboard)/dashboard/configuration/page.tsx",
      "src/app/(dashboard)/terminal/page.tsx",
      "src/components/assistant/agent/chat-list.tsx",
      "src/components/database/data/field.tsx",
      "src/components/docker/container-actions.tsx",
      "src/components/docker/stack-row.tsx",
      "src/components/files/editor-toolbar.tsx",
      "src/components/files/file-tree.tsx",
      "src/components/files/more-menu.tsx",
      "src/components/network/captures.tsx",
      "src/components/network/interfaces/device-identity.tsx",
      "src/components/proxy/engine.tsx",
      "src/components/xterm-pane.tsx",
    ],
  },
  {
    why: "A column's width still asked of the window: it becomes a container query with its page (FND-14).",
    ids: ["media-literal"],
    files: [
      "src/components/database/data/index.tsx",
      "src/components/docker/beside-rail.ts",
      "src/components/docker/container-detail.tsx",
      "src/components/docker/container-storage-tab.tsx",
      "src/components/docker/network-views.tsx",
      "src/components/docker/stack-preview.tsx",
      "src/components/docker/stack-usage-band.tsx",
      "src/components/network/routing/bgp.tsx",
      "src/components/network/routing/route-tables.tsx",
      "src/components/network/vpn/tailnet-machines.tsx",
    ],
  },
  {
    why: "A row of tiles still to be said in words (PRM-29).",
    ids: ["stat-grid"],
    files: ["src/components/deploy/run-railpack.tsx", "src/components/deploy/runtime-usage.tsx"],
  },
]

/** The table as config: one block per file, its ids split between the two rules. */
function notYet(table) {
  const byFile = new Map()
  for (const group of table) {
    const entries = Array.isArray(group.files)
      ? group.files.map((file) => [file, group.ids])
      : Object.entries(group.files)
    for (const [file, ids] of entries) byFile.set(file, [...(byFile.get(file) ?? []), ...ids])
  }
  return [...byFile].map(([file, ids]) => {
    const retired = ids.filter((id) => RETIRED_IDS.includes(id))
    const tokens = ids.filter((id) => !RETIRED_IDS.includes(id))
    const rules = {}
    if (tokens.length > 0) rules["design-system/tokens"] = ["error", { off: tokens }]
    if (retired.length > 0) rules["retired-apis/retired"] = ["error", { off: retired }]
    return { files: [file], rules }
  })
}

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
  {
    plugins: { "design-system": designSystem, "retired-apis": retiredApis },
    rules: {
      "design-system/tokens": "error",
      // The components and habits the 0.9.0 UI-system pass retired: each id names the shared
      // component that owns the pattern (eslint-rules/retired-apis.mjs).
      "retired-apis/retired": "error",
      // A date printed with options builds a fresh Intl.DateTimeFormat on
      // every call, which on a chart's axis was a quarter of what the Metrics
      // page cost while it sat open. `dateFormatter` in lib/format.ts builds
      // one and keeps it; the shared shapes are exported beside it. A named
      // locale ("en-GB", for a schedule read in its own zone) is left alone:
      // those are one label each, with a zone only known when they are drawn.
      "no-restricted-syntax": [
        "error",
        {
          selector:
            "CallExpression[callee.property.name=/^toLocale(Date|Time)String$/][arguments.0.type='Identifier'][arguments.0.name='undefined'][arguments.length>1]",
          message:
            "Print a date through a format built once: clockAt, minuteAt, dayAt and the others in lib/format.ts, or dateFormatter(options) declared at the top of the module.",
        },
        {
          selector:
            "CallExpression[callee.property.name='toLocaleString'][arguments.0.type='Identifier'][arguments.0.name='undefined'] > ObjectExpression.arguments > Property[key.name=/^(era|year|month|day|weekday|hour|minute|second|hour12|hourCycle|dateStyle|timeStyle|timeZone|timeZoneName|fractionalSecondDigits)$/]",
          message:
            "Print a date through a format built once: clockAt, minuteAt, dayAt and the others in lib/format.ts, or dateFormatter(options) declared at the top of the module.",
        },
        // An animation that never ends is the browser's to run (a CSS
        // animation, or one handed to the Web Animations API): motion sets an
        // endless one from a JavaScript frame loop, and a page with a wire on
        // it was then never idle (design system §11, *live*).
        {
          selector: "Property[key.name='repeat'][value.name='Infinity']",
          message:
            "An endless animation is written in CSS (see jd-beam, jd-shimmer and animate-sweep in globals.css), not with motion's repeat: Infinity, which runs a frame loop for as long as the page is open.",
        },
      ],
      "design-system/latency-words": "error",
      "design-system/primitives": "error",
      // Reaching past a primitive to the thing it wraps is the other mechanical
      // failure. Each of these has exactly one legitimate assembler, listed in
      // the override below.
      "no-restricted-imports": [
        "error",
        {
          paths: [
            {
              name: "@/components/ui/dialog",
              message:
                "Raw Dialog is assembled only by components/modal.tsx. A page opens a Modal or a PaletteModal.",
            },
            {
              name: "@/components/ui/sheet",
              message:
                "Raw Sheet is assembled only by components/side-panel.tsx. A page opens a SidePanel.",
            },
            {
              name: "@heroicons/react/24/solid",
              message: "Icons come from components/icons.tsx, which defines the vocabulary.",
            },
            {
              name: "@heroicons/react/24/outline",
              message: "Icons come from components/icons.tsx.",
            },
            {
              name: "sonner",
              message: "Toasts go through lib/toast.ts, which owns what a failure says.",
            },
            {
              name: "motion/react",
              importNames: ["useReducedMotion"],
              message:
                "useReducedMotion comes from components/shell/motion-pause.ts: it follows Pause animations and the system's setting while the component is on screen, where motion's reads once at mount.",
            },
          ],
        },
      ],
    },
  },
  {
    // Keyboard and screen-reader access, as far as it can be read off the
    // markup. eslint-config-next turns on six of jsx-a11y's rules as warnings;
    // this is the plugin's whole recommended set as errors, which found an
    // unlabelled field and some forty elements a mouse could press and a
    // keyboard could not. A rule that is wrong for this codebase is turned
    // off below with the reason beside it, and a single place where a rule
    // misreads the markup carries the reason on its own disable comment.
    files: ["src/**/*.tsx"],
    rules: {
      ...jsxA11y.flatConfigs.recommended.rules,
      // The product's own controls are controls: a <label> that wraps a
      // Checkbox or a Switch and its words is labelled, as one that wraps an
      // <input> is.
      "jsx-a11y/label-has-associated-control": [
        "error",
        {
          controlComponents: [
            "Checkbox",
            "Switch",
            "Input",
            "Textarea",
            "Select",
            "SelectTrigger",
            "RadioGroupItem",
            "Slider",
          ],
          depth: 4,
        },
      ],
      // `role` on one of the product's own components is that component's
      // prop (a release's part in a canary), not an ARIA role.
      "jsx-a11y/aria-role": ["error", { ignoreNonDOM: true }],
      // A press is what a keyboard can be left out of, so a press is what
      // these two look for. A key handler on a container is the opposite: it
      // hears the keys of the focusable things inside it (the arrows of a
      // list that moves its own focus, a canvas's shortcuts) and has nothing
      // a pointer could do that a keyboard could not.
      "jsx-a11y/no-static-element-interactions": [
        "error",
        { allowExpressionValues: true, handlers: ["onClick", "onMouseDown", "onMouseUp"] },
      ],
      "jsx-a11y/no-noninteractive-element-interactions": [
        "error",
        {
          handlers: ["onClick", "onError", "onLoad", "onMouseDown", "onMouseUp"],
          body: ["onError", "onLoad"],
          iframe: ["onError", "onLoad"],
          img: ["onError", "onLoad"],
        },
      ],
      // What may take the keyboard without being a control: a separator that
      // does is ARIA's window splitter, moved with the arrows; a region, a
      // log or a group that scrolls has to be reachable for its arrows to
      // scroll it (WCAG 2.1.1); the remote screen is an application that
      // takes every key.
      "jsx-a11y/no-noninteractive-tabindex": [
        "error",
        {
          tags: [],
          roles: ["tabpanel", "separator", "region", "log", "group", "application"],
          allowExpressionValues: true,
        },
      ],
      // Off: every autoFocus here is the field a dialog, a sheet or an inline
      // editor opens on, or the first field of the sign-in page, which is what
      // the person just asked to type into. The rule is against a page that
      // takes the keyboard on load for something the reader did not ask for,
      // and nothing here does that.
      "jsx-a11y/no-autofocus": "off",
      // Off: the players are for the operator's own files and a repository's,
      // read from the server's disk. There is no caption track to offer for a
      // file nobody wrote captions for.
      "jsx-a11y/media-has-caption": "off",
    },
  },
  {
    // The components that legitimately assemble what everything else composes.
    files: [
      "src/components/modal.tsx",
      "src/components/side-panel.tsx",
      "src/components/icons.tsx",
      "src/lib/toast.ts",
      "src/components/ui/sonner.tsx",
      // Generated shadcn primitives that wrap Dialog/Sheet themselves.
      "src/components/ui/command.tsx",
      "src/components/ui/sidebar.tsx",
    ],
    rules: { "no-restricted-imports": "off" },
  },
  {
    // A test prints a date the slow way to say what the built-once format must match.
    files: ["src/**/*.test.js"],
    rules: { "no-restricted-syntax": "off" },
  },
  {
    // A test, a codemod and the rules themselves hold the old spellings on purpose: as the input
    // a function or a page is checked against, as the table a migration rewrites from, and as
    // what a rule refuses.
    files: [
      "src/**/*.test.js",
      "src/**/*.test.ts",
      "tests/**",
      "scripts/codemods/**",
      "eslint-rules/**",
      "eslint.config.mjs",
    ],
    rules: { "design-system/tokens": "off", "retired-apis/retired": "off" },
  },
  ...notYet(NOT_YET),
  {
    // Where a value is *defined* rather than consumed. The generated shadcn
    // primitives own the control faces; `logo.tsx` owns the wordmark's own ramp;
    // `components/metrics/`, with the sparkline and the chart panel it shares,
    // owns the chart palette; `icon-action.tsx` owns the reveal rule itself.
    files: [
      "src/components/ui/**",
      "src/components/icon-action.tsx",
      "src/components/logo.tsx",
      "src/components/metrics/**",
      "src/components/sparkline.tsx",
      "src/components/chart-panel.tsx",
      "src/components/logs/histogram.tsx",
      "src/components/git/graph-panel.tsx",
      "src/components/database/diagram/**",
      "src/components/xterm-pane.tsx",
    ],
    rules: { "design-system/tokens": "off" },
  },
  globalIgnores([
    // Default ignores of eslint-config-next:
    ".next/**",
    "out/**",
    "build/**",
    "next-env.d.ts",
    // Browser reports bundle third-party trace viewers, not application source.
    "playwright-report/**",
    "test-results/**",
    // The Monaco runtime, copied in from node_modules by
    // scripts/sync-monaco.mjs. It is a vendored build, not source: linting it
    // buries every real finding under twenty-five thousand from minified code
    // nobody here wrote.
    "public/monaco/**",
  ]),
])

export default eslintConfig
