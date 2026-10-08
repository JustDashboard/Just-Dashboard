# Mounted observer stop authorization

The integrated `Server.Routes()` surface uses the same explicit-observer-stop contract as the narrow
flow handler fixture. `TestNetworkReportMountedObserverStopPrerequisite` calls the reusable scenario
on actual mounted routes: GET and ordinary history opt-in never attach an observer, explicit observer
activation requires history recording, ordinary history opt-out refuses to detach an active
observer, explicit destructive stop succeeds, and only then ordinary history opt-out succeeds.
Exactly two successful observer mutations are audited.

On the integrated source containing `6017389b` and `e50779a4`, Go 1.26.8:

```bash
cd backend
GOMAXPROCS=2 go test -race -p 2 ./internal/api \
  -run '^TestNetworkReportMountedObserverStopPrerequisite$' -count=1
```

**PASS: 4.355 seconds.** Full output is retained in
`/home/ubuntu/jd-network-validation-tmp/reservation-lifecycle/mounted-observer-api-race.log`.
This route regression does not replace the real kernel observer fixtures or the fresh integrated
frontend/browser gate before pushing.
