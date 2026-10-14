# Documentation and boundary review

The complete change was compared with `docs/internal/`, `AGENTS.md`, `README.md` and
`CONTRIBUTING.md` before publication.

- Board and Stickyboard guides now describe memory retention, original revisions, pending-save
  ownership, unload warnings and account cleanup.
- Proxy guidance describes required raw-save revisions, reviewed retries, guarded validation rollback,
  pinned native staging and canonical password-directory exclusions.
- Files guidance describes exact image snapshots, the 64 MiB read bound, guarded uploads, pinned
  cleanup/publication, allowed/outward retarget outcomes and the native comparison window.
- Backup and deployment coverage guides describe one-statement settings/credential publication,
  additive revisions, guarded editor review and narrow native-dump additions.
- Authentication/data guidance covers untouched successful streams, shared refusal observation,
  XHR progress/cancellation, late image-save cleanup, account-keyed workspace remounts and raw
  editor ownership checks before validation/publication. Account and observability guides describe
  required-factor policy and fail2ban-specific evidence.
- The audit matrix preserves the nine original findings, the already-merged T-01 correction,
  before/after evidence, recovery recordings and all twelve operational limits. Its final verification
  records broad results and scoped corrections without presenting a failed run as green.

No update is needed to `AGENTS.md`, `README.md` or `CONTRIBUTING.md`: installation, environment
settings, existing commands, contribution/licensing rules and release workflow remain accurate.
There are no dependency, CI, release-version, changelog or `/network` page changes.

Backend capabilities, CSRF checks, audit/destructive middleware and typed confirmations retain their
contracts. Backup schema changes are additive with a default through `store.addedColumns`; no shipped
migration is removed. Files and proxy operations keep path resolution and now pin temporary-file
metadata/publication/cleanup to an approved directory. The existing proxy password-reader exclusion
is retained through relative roots and aliases. No invariant is intentionally weakened.
