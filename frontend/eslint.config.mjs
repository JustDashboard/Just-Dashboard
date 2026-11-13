import { defineConfig, globalIgnores } from "eslint/config"
import nextVitals from "eslint-config-next/core-web-vitals"
import nextTs from "eslint-config-next/typescript"
import jsxA11y from "eslint-plugin-jsx-a11y"

import designSystem from "./eslint-rules/design-system.mjs"

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
  {
    plugins: { "design-system": designSystem },
    rules: {
      "design-system/tokens": "error",
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
    // Where a value is *defined* rather than consumed. The generated shadcn
    // primitives own the control faces; `logo.tsx` owns the wordmark's own ramp;
    // `components/metrics/` owns the chart palette; `icon-action.tsx` owns the
    // reveal rule itself.
    files: [
      "src/components/ui/**",
      "src/components/icon-action.tsx",
      "src/components/logo.tsx",
      "src/components/metrics/**",
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
