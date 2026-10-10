# Network snapshot absence durability

A durable network recovery snapshot can record that an owned file was absent
before the candidate apply. Restoring that snapshot unlinks the candidate and
syncs its exact containing directory before recovery may become terminal. This
also applies to sysctl and unit directories outside the journal's own directory.
An already absent entry still flushes an existing parent, so recovery can finish
a prior failed directory sync. A missing parent remains absent; recovery creates
no directory to manufacture success.

Directory-open or sync failure is propagated into the journal's recovery errors
and degraded, nonterminal state. A focused I/O-failure regression proves that a
successful unlink followed by failed sync cannot report recovered, and that a
later successful retry can complete. These checks do not prove actual power-loss
or reboot acceptance.

```sh
cd backend
GOMAXPROCS=2 go test ./internal/netx -run '^TestAbsentSnapshot'
```
