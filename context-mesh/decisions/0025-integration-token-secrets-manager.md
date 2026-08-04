# 0025 – Integration.Token → AWS Secrets Manager

## Status

Accepted · (date: 2026-08-03)

## Context

`Integration.Token` (the *outbound* bearer credential the Hub would use to
call a cluster's k8fy-adapter) has been stored plaintext in Postgres since it
was prototyped — the struct itself carried a comment flagging this:
"Token is stored plaintext in Postgres for the prototype; in production it
should be a reference to a Secrets Manager secret ID." ROADMAP P16 tracked
this as its last open sub-problem, after sub-problems (1) and (2) (namespace→
cluster routing, ingested-data cluster scoping) were resolved by ADR
0023/0024.

Confirmed by direct code inspection before designing this: **the token has no
runtime consumer today.** Outbound adapter calls go through a single global
`AdapterClient` built once at startup from env vars (`ADAPTER_URL`/
`ADAPTER_AUTH_TOKEN`), never from a per-Integration row — that wiring
(a keyed, per-Integration `AdapterClient`) is a separate, not-yet-built piece
that ADR 0023/0024 deliberately did *not* resolve (P16 sub-problem (2) was
closed instead via cluster-aware pod IDs, a different mechanism entirely).
So this ADR is scoped to storage hygiene — closing the "plaintext secret
sitting in the DB, exposed via `PUT /admin/integrations/{id}`" gap — not to
building the outbound consumer.

**Deliberately out of scope: `Integration.CollectorToken`.** That field is
the *inbound* push credential Discovery presents on every request, resolved
via `GetIntegrationByCollectorToken`'s `WHERE collector_token = $1` equality
lookup — the hot path of every collector push and live-fetch relay. A
Secrets-Manager-backed value cannot serve as a Postgres lookup key without a
redesign (e.g. a hash column for lookup, the real value in Secrets Manager) —
a structurally different problem from `Token`'s write-once-read-rarely
shape. Not solved here.

## Decision

**Gate on one env var; empty reproduces today's plaintext behavior exactly.**
`INTEGRATION_SECRETS_PREFIX` (e.g. `agentify/dev/integrations`) on the Hub.
Empty (the default, every existing deployment) → `Token` is read/written
plaintext, byte-for-byte unchanged. Set → new/updated tokens are stored in
AWS Secrets Manager instead. Same "empty = unscoped/unchanged" idiom already
used for `cluster_id` throughout ADR 0023/0024.

**New `internal/secrets` package** — a small `Manager` interface
(`Store`/`Fetch`/`Delete`) wrapping `*secretsmanager.Client`
(`github.com/aws/aws-sdk-go-v2/service/secretsmanager`, added the same way
`service/dynamodb` already was). `Store` creates the secret on first write and
falls back to updating it (`PutSecretValue`) on a subsequent write to the
same deterministic name (`{prefix}/{integration_id}`) — an admin editing an
existing row's token reuses its secret rather than accumulating a new one
per edit.

**Schema:** additive `token_secret_arn TEXT NOT NULL DEFAULT ''` column
(idempotent `ALTER TABLE`, same style as every other migration in
`initSchema()`). `Token` and `TokenSecretARN` are **mutually exclusive** —
`UpdateIntegration`'s SQL clears whichever one isn't being written this call,
so a row transitioning between plaintext and Secrets-Manager mode never ends
up with a stale plaintext token sitting alongside a live secret reference.

**Handler wiring:** `Handler` gained one more nilable field,
`secretsManager secrets.Manager` (nil = nothing configured — the same
"nil when not provisioned" convention as `integrationStore`/`traceStore`/
etc.). `HandleIntegrationCreate`/`HandleIntegrationUpdate` route a non-empty
incoming token through `Store` when a manager is configured, saving the ARN
instead of the plaintext value; `HandleIntegrationDelete` best-effort deletes
the secret after the row is gone (logged, never fails the request — a
cleanup failure shouldn't block deleting the integration itself).

**One-time migration script** (`cmd/migrate-integration-tokens`): for rows
created before this shipped, moves each existing plaintext token into
Secrets Manager and clears the column. Must be run once, immediately after
setting `INTEGRATION_SECRETS_PREFIX`, before any further admin edits to
existing rows — editing a row under Secrets-Manager mode before running the
migration clears that row's old plaintext token as a side effect (per the
mutual-exclusion rule above) without first preserving it in Secrets Manager,
losing the credential rather than migrating it.

**Terraform:** one new statement on the existing `backend_secrets` IAM policy
(`CreateSecret`/`PutSecretValue`/`GetSecretValue`/`DescribeSecret`/
`TagResource`/`DeleteSecret`, scoped to the
`.../secret:{project}/{env}/integrations/*` ARN prefix) — mirroring the
CI role's pre-existing Langfuse-secret grant, the only other role in this
repo that both creates and reads secrets rather than only reading a
Terraform-known one. No new `aws_secretsmanager_secret` resource: these are
per-row and created dynamically by the app at runtime, unlike the `db`/
`anthropic`/`adapter`/`langfuse` secrets Terraform provisions up front. No
Kubernetes manifest change — IRSA already grants the backend pod this role.

## Consequences

- **Positive:** closes P16's last open sub-problem; zero behavior change for
  every deployment that hasn't set `INTEGRATION_SECRETS_PREFIX`; the
  mutual-exclusion invariant means a row can never silently carry both a
  plaintext token and a live secret reference at once; the admin UI
  (`IntegrationsPanel.tsx`) needed no change at all — it already never
  round-trips the token value.
- **Negative / cost accepted:** the migration script must be run before any
  post-cutover edits to pre-existing rows, or an old plaintext token can be
  lost rather than migrated (documented above and in the script's own
  header comment). `Integration.Token` still has no runtime consumer after
  this ADR — the per-Integration outbound `AdapterClient` that would actually
  *use* this credential remains unbuilt (tracked separately, not reopened
  here).
- **Revisit if:** the per-Integration outbound `AdapterClient` work happens —
  at that point `Manager.Fetch` (already part of the interface, unused until
  then) becomes load-bearing instead of just completing the interface's
  lifecycle; or if `CollectorToken` ever needs the same treatment, which
  will require the lookup-key redesign flagged above, not a copy of this
  approach.
