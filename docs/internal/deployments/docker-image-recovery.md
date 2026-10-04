# Recovering an existing container whose image was deleted

Managed adoption first pins each existing service's exact local image ID. When an original image was
force-deleted but its container still exists, `dockerx.RecoverAdoptionImage` can preserve that container's
root filesystem as a new local recovery image. This path needs an authoritative Linux amd64 or arm64
platform descriptor retained by Docker's container inspection; it never guesses architecture from the
host or a binary. Missing descriptors, unsupported platforms, and meaningful writable-layer changes
block recovery with a reason to restore the original image or supply its source.

The adapter uses Docker's read-only container export API. It does not pause, stop, restart or signal the
application, execute a command inside it, or use `docker commit`. Docker export excludes mounted volume
contents. The existing mounts are recovered separately as external volumes, contained bind paths and
existing network identities. An export is not a database backup. Review and verify a persistent-data
backup before Deploy changes because an image/configuration rollback cannot undo database, schema or
file changes in those existing mounts.

Only the already verified regenerable Python bytecode additions are removed from the captured archive.
Unknown additions, source modifications, unverified directory changes and writable-layer data still
block adoption. The archive preserves rootfs metadata and is capped at 2 GiB and 100,000 entries. It is
staged in a private cache directory, using regular files with mode 0600 and rejecting symlink artifacts.
No archive is returned to the browser.

Image import sets only the dashboard's source-identity label. Captured environment variables,
entrypoint, command and original labels stay in encrypted deployment inputs, outside the imported image
configuration. The imported tag is `just-dashboard/adoption-recovery:<source-hash>`. A private manifest
records its exact image ID, source hash, archive hash and platform. The source hash fences container ID,
original image identity, effective configuration, resolved mounts/networks, process PID/start time,
platform descriptor and writable-layer diff. Recovery checks the fence before and after export;
ordinary health polling does not change the identity. Retrying recovery reuses the exact owned image
after checking its ID, controlled label and absence of environment/command configuration. The adoption
API additionally checks a fresh captured baseline before committing ownership.

A declared Compose service without an existing container cannot use this fallback. In the default
`all_services` scope, a missing image blocks recovery with `inactive_service_image_missing`; the
dashboard cannot invent that service's source or filesystem. An explicit `existing_services` scope
may omit only declarations with no container after reviewing each named exclusion and acknowledging
its warning. Every existing stopped and running container remains included. Relationships from a
retained service to an exclusion block recovery rather than being removed. Original Compose files
and excluded resources remain unchanged. Adoption preserves stopped containers and absent services exactly.
Deploy changes explicitly applies the reviewed recipe. Replica-specific storage or network identities
that cannot be faithfully expressed as one Compose service also block recovery.

Keep the recovery image and private source cache available for future deployments and rollback.
Isolated proof tooling may clean up only the exact images recorded in its own cache manifests, after
checking `io.just-dashboard.adoption-source` matches each manifest. It must not remove an operator's
original image, prune the image store, or infer ownership from an arbitrary image name.

The opt-in tests create and clean up only their own loopback HTTP applications, volumes, networks and
unique images. `TestLiveDeletedImageExportDoesNotChangeOriginalRuntime` checks export continuity and
secret-free raw import. `TestLiveManagedDeletedImageContainerAdoptionAndRollback` exercises the
production cached fallback, repeated recovery, nonmutating adoption, failed Deploy restoration,
successful managed deployment and rollback while preserving HTTP, persistent data and network aliases:

```bash
cd backend
JD_DOCKER_ADOPTION_LIVE=1 JD_ADOPTION_EVIDENCE_DIR=/tmp/jd-managed-adoption-evidence \
  go test ./internal/deploy -run '^TestLiveManagedDeletedImageContainerAdoptionAndRollback$' -count=1 -v
```

Docker documents [export's volume exclusion](https://docs.docker.com/reference/cli/docker/container/export/),
[import's supported configuration changes](https://docs.docker.com/reference/cli/docker/image/import/),
and [commit's default pause behavior](https://docs.docker.com/reference/cli/docker/container/commit/).

## Verified n8n generated files

Standard n8n startup copies transformed editor JavaScript, CSS and `index.html` into
`/home/node/.cache/n8n/public`, and rebuilds three node/credential **type definition** JSON files.
The capture adapter recognizes only exact reviewed startup generator hashes, currently verified
against n8n 2.39.10 with its default entrypoint and start command. It checks the immutable editor
distribution and current cache through bounded archives (128 MiB and 4,096 regular file/directory
entries each), requiring each added output to be a shipped generated filename or one of those three
type files. Only positively verified directory ancestors are exempted. Unknown versions/generators,
unknown files/directories, changed or deleted outputs, symlinks, hardlinks and source modifications
retain the writable-layer blocker. Files outside these exact paths receive the ordinary data check.

`/tmp/n8nDataTableUploads` is permitted only when newly created and independently verified empty.
Any upload file is application data and blocks adoption until persisted/backed up. Workflow state,
the encryption key and real encrypted credentials remain in n8n's existing mounted storage; the
cache exception never substitutes for preserving that storage or verifying a backup.

The source rationale is n8n's pinned
[static-asset generator](https://github.com/n8n-io/n8n/blob/n8n%402.39.10/packages/cli/src/commands/start.ts),
[type generator](https://github.com/n8n-io/n8n/blob/n8n%402.39.10/packages/cli/src/services/frontend.service.ts),
and [upload middleware](https://github.com/n8n-io/n8n/blob/n8n%402.39.10/packages/cli/src/modules/data-table/multer-upload-middleware.ts).
The owned live source-proof fixture verified all 1,595 observed changes while container ID, PID,
start time and configuration stayed identical. `TestLiveHeldN8nCacheProof` is read-only and opt-in
through `JD_N8N_CACHE_PROOF_CONTAINER`, and accepts only a held `jd-n8n-cache-proof-*` fixture.
