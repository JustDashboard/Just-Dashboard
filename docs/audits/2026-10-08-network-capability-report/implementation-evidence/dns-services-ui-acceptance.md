# Native DNS service UI acceptance

This checkpoint integrates P17's private native connection, inventory, retained review and owned
setup/removal controls on `/network/dns`. It preserves the reading register and the existing host
resolver controls. An unavailable host resolver does not prevent an administrator from opening the
independent native service inventory. A read user never requests the private service routes.

## Source and matching build

The mounted application is `628b1ec32e4be1256b800eb772025bb5baa08796`, with the explicit empty
shared-fixture inventory at `35f58b7faf38fa681e89f73a3e29f529552603af`. The
[production build](dns-services-ui-build.txt) passed compilation, TypeScript and all 73 page outputs.
Its server binds `127.0.0.1:43145`. Subsequent commits add native backend safety and browser assertions;
they do not change the frontend application served by this build.
Checked-in build text removes terminal carriage returns/trailing whitespace only; the original remains
at `/home/ubuntu/jd-network-validation-tmp/dns-services-ui-build.txt`.

The [full fast logic layer](dns-services-ui-bun-src.txt) passed 3,232 tests, 14,008 assertions and
239 files. [Focused parser/review guards](dns-services-ui-pure-final.txt),
[TypeScript](dns-services-ui-tsc-final.txt) and [ESLint](dns-services-ui-eslint-final.txt) also passed.
The guards check current owner generation/fingerprint, immutable reviewed intent, explicit terminal
states, expiry and exact owned resources. Saved attempt identifiers contain no native credentials.

## Mounted interactions

The [initial selected DNS service spec](dns-services-ui-browser-initial.txt) passed all 13 cases in
33.6 seconds. The [required changed-spec gate](dns-services-ui-browser-required.txt), against the
agent's `f8b97789` integration of the mounted application, passed static checks, the fast logic layer
and the same 13 cases in 33.1 seconds. Both use the matching production server and one browser worker.

The cases cover private reader access, read-only defaults, credential replacement without origin
changes, credential re-entry after failure, complete rendered native evidence, read-only staging
refusal, upstream draft retention through preview, expiry, stale native reads, changed same-ID
reviews and failed owner reads while confirmation is open, HTTP 200 `needs_review`, one apply after
a lost response and reload, and exact owned container/bridge/two-volume removal.

The additional [review viewport assertion](dns-services-ui-review-viewport.txt) passed against the
unchanged application: moving from the long upstream inventory to a retained review opens with its
reviewed metadata in the viewport. No application correction was needed for that hypothesis.

The combined backend/frontend gate against `ac9d6e42` is recorded separately after completion.
These focused passes do not claim that an unfinished combined invocation succeeded.

## Visual inspection

The page, inventory, connection form, owned setup form and retained setup review were captured at
390, 1280 and 1720 pixels. All 15 captured surface/width measurements had equal horizontal client and
scroll widths; see [dimensions](dns-services-ui-dimensions.json) and the
[final capture output](dns-services-ui-capture-final.txt). The inspected images include the
[desktop service lists](dns-service-page-1720.png), [phone service lists](dns-service-page-390.png),
[native inventory](dns-service-inventory-1280.png), [phone native views](dns-service-views-390.png),
[connection form](dns-service-connect-1280.png), [phone setup form](dns-service-provision-390.png),
and retained owned resources on [phone](dns-service-owned-review-390.png) and
[desktop](dns-service-owned-review-1720.png).

These are explicitly mocked UI fixtures. The final inventory images use a Technitium fixture for
authoritative zone/view examples; they are not proof that another engine supports those roles.
Unknown runtime, configured policy and unmeasured client selection remain labeled separately.

The [first capture attempt](dns-services-ui-capture-initial-invalid-base-url.txt) failed before
navigation because its new browser context omitted `baseURL`. The capture harness was corrected;
no application behavior or acceptance assertion was suppressed.

## Remaining scope

Actual pinned AdGuard, Pi-hole and Technitium compatibility evidence is maintained separately in
the [native engine record](../../../internal/backend/evidence/dns-services-2026-10-09/README.md).
The UI exposes the four existing closed native actions, retained native query evidence and bounded
owned provisioning. Full query/zone/view/client-policy editing, broader platform behavior, host
reboot and complete P17 acceptance remain open in the [ledger](../implementation-status.md).

Before push, the complete change must be compared with the internal guides, `AGENTS.md`, `README.md`
and `CONTRIBUTING.md`. This record is acceptance for the described slice, not a 10/10 product score.
