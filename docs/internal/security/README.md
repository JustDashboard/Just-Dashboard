# Security guides

- [`invariants.md`](invariants.md) — mandatory security/compatibility invariants, typed-confirmation policy, and what a role is and is not (every role reads what root reads; `limited` and `admin` are root).
- [`master-key.md`](master-key.md) — what `JD_MASTER_KEY` seals and derives, where sealed values and copies of the key are, and what a rotation has to do and prove before it can ship (it cannot be changed yet).
- [`../architecture/request-lifecycle.md`](../architecture/request-lifecycle.md) — middleware and route enforcement.
- [`../architecture/runtime-boundaries.md`](../architecture/runtime-boundaries.md) — host execution, paths, auth, secrets, audit, and state.
