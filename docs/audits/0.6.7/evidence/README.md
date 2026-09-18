# Remediation verification evidence

These records cover the `patch/0.6.7` fixes described in
[the remediation report](../remediation-report.md). They supersede the earlier audit results only for
checks explicitly rerun; they are not proof of every deployment or host configuration. Captured text
logs have terminal line endings and trailing whitespace normalized for inclusion in Git.

- Backend: Go 1.26.8 built from the official source tag. The final full suite uses isolated PostgreSQL,
  MariaDB and Redis containers with temporary test credentials. Unavailable SQL Server, MongoDB,
  ClickHouse and Oracle integrations skip or use unit fakes; no user databases are targeted.
- `jd-fixes-go-test-final.txt`: final full backend suite.
- `jd-fixes-go-build.txt` / `jd-fixes-go-vet.txt`: final build and vet (exit 0; empty output).
- `jd-fixes-deployment-race.txt`: required deploy/API/proxy/backups/store race suite.
- `jd-fixes-files-acl.txt`: complete files package race suite after the final ACL preservation follow-up.
- `jd-fixes-db-files-race.txt`: SQL statement and SHOWPLAN cleanup plus rooted copy/move race checks.
- `jd-fixes-postgres-sizes-api.txt`: live PostgreSQL overview and generator surface after NULL-size fix.
- `jd-fixes-deploy-live.txt`: opt-in C4 build/artifact and C5 activation Docker fixtures, with cleanup.
- `jd-fixes-root-race.txt`: focused auth, HTTP, lifecycle, updates and migration race checks.
- `jd-fixes-precision-live.txt`: actual PostgreSQL/MariaDB BIGINT and DECIMAL(40,20) round trips.
- `jd-fixes-frontend-unit-final.txt`: 21 unit tests, 53 assertions.
- `jd-fixes-frontend-browser-final.txt`: earlier complete Chromium run, 94 passes.
- `jd-fixes-frontend-compose-browser.txt`: final Docker coverage, 26 passes. Its combined command also
  records two new security fixture failures caused by missing Docker ping mocks; the corrected final
  security run below supersedes those failures.
- `jd-fixes-frontend-compose-security.txt`: final 10 security browser cases pass, including enrollment
  and Compose permission changes. Across the complete and focused runs, 97 unique cases passed.
  Browser tests intercept APIs and validate browser behavior, not live backend integration.
- `jd-fixes-frontend-compose-lint.txt` / `jd-fixes-frontend-compose-build.txt`: final frontend gates.
- `jd-fixes-govulncheck-final.txt`: verbose scan after Go and x/crypto upgrades. Nonzero, with two
  daemon-related Docker symbol results and four module-only findings; applicability is in the report.
- `jd-fixes-frontend-audit.json`: four remaining DOMPurify advisories in Monaco's bundled sanitizer;
  applicability is in the report and internal editor documentation.

Empty build/vet logs indicate a successful command only when paired with the recorded zero exit
status in the report. All commands ran without committing, pushing or replacing the running dashboard.
