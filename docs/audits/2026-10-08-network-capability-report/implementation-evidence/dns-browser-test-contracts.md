# Retained DNS evidence browser contract

The combined gate on application source `b8050335` found one retained-report assertion
that expected a paragraph to contain only its evidence label. The paragraph also renders
its evidence basis, as shown in the retained failed snapshot. Encryption, DNSSEC and TLS
trust labels were present; the exact basis-free text locator did not match that paragraph.

The corrected test finds each semantic definition by its existing evidence label and
also checks its native basis. It preserves the answer, endpoint uncertainty, NSS/provider
scope and export assertions. No application source, DNS behavior, displayed content,
fixture result or native trust interpretation changes.

The required selected gate passed against the source-matched production server on port
43139:

```sh
JD_BROWSER_BASE_URL=http://127.0.0.1:43139 JD_BROWSER_WORKERS=1 \
  scripts/test-changed.sh b80503350e3f14ff345527bbc1a7a166f963b470
```

Changed-file Prettier/ESLint and TypeScript passed. All 3,131 Bun tests passed, and all
seven selected DNS evidence browser cases passed in 13.5 seconds, including both alias
layouts, explicit launch, retained failed launch, interrupted history and read-only
access. This is corrected-test proof against the same built application; it does not
convert the original failed combined invocation into a passing invocation.

Documentation review compared the test-only diff with `docs/internal/`, `AGENTS.md`,
`README.md` and `CONTRIBUTING.md`. No behavior, security, configuration, schema, command or
workflow update is needed. Detailed DNS scope remains in the existing native DNS guide;
broader owner, client-vantage and foreign-chain unknowns remain open in F10/P9.
