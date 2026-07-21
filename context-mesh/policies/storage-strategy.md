# Policy: Storage Strategy

> **Question this answers:** Given an incoming data event, *where* and *how*
> should the system store it for the most efficient future retrieval?
>
> One of the four "brain" policies.
>
> **Design principle — classify, don't enumerate.** Event types are fluid (new
> use cases and integrations appear constantly), so we never hardcode a list of
> event types. Instead, storage is a **pure function of a small set of intrinsic
> event traits**. New event types just get classified by those traits; this
> policy never needs editing when an integration is added.
>
> ✅ **Resolved (2026-06-02, [ADR 0010](../decisions/0010-postgres-single-store.md)).**
> The *trait → store family* classification below **stays** (as does the pod-mesh
> concept), but the MVP **backs every family with a single Postgres**: `kv`
> (current-state) → a `current_state` table (upsert latest-wins); `relational`
> (append-only) → the `events` table. Redis is removed. `vector` (pgvector),
> `timeseries`, `logs`, and `graph` are **not provisioned** — defer until a feature
> or volume justifies them (route there only when added). So the decision function
> is unchanged; only the number of running engines shrank from "up to six" to one.

## Principle

_<state your guiding rule in one sentence, e.g. "Choose the store that matches an
event's dominant access pattern; for events we don't own, store a queryable
mirror rather than the master copy; default to the cheapest store that answers
fast.">_

## Step 1 — Classify the event by traits (not by name)

Every event, regardless of domain or source, is described by ~5 orthogonal traits.
These — not the event's name — decide storage.

| Trait | Values | Why it matters |
|-------|--------|----------------|
| **Shape** | structured · semi-structured · freeform-text · numeric/metric | First cut at the store family |
| **Access pattern** | point-lookup · similarity/semantic · filter+aggregate · relationship-traversal · time-range-scan | The *strongest* determinant of store type |
| **Temporality** | current-state (snapshot) · append-only stream · time-series | "Latest value" vs "everything that happened" |
| **Mutability** | immutable · mutable · ephemeral (TTL) | Drives store choice + tiering |
| **Authority** | system-of-record · derived/cache/mirror | Are we the truth, or a queryable copy of someone else's truth? |

> Tip for filling the Principle / tuning thresholds: **write the 3–5 example
> queries you expect, then read the traits backward from how data is asked for.**

## Step 2 — Map traits → store family (the decision function)

This table stays valid forever because it keys on traits, not event types.

| Access pattern + temporality | Store family |
|------------------------------|--------------|
| similarity / semantic (any freeform text) | **vector** |
| point-lookup, current-state | **KV** |
| filter + aggregate, structured | **relational / columnar** |
| relationship-traversal | **graph** |
| time-range-scan, time-series / metric | **time-series (TSDB)** |
| time-range-scan, append-only logs / audit | **log / search index** |

> **v1 realization (ADR 0013):** this table is the *target*. For the MVP the
> `time-series (TSDB)` and `log / search index` families are **realized in the
> Postgres events table** (no separate datastore yet) — `GetBackend` aliases the
> `timeseries`/`logs` store types to the relational events backend. The traits and
> routing are unchanged; only the realized backend differs. Revisit at scale.
>
> **First real `log / search index` realization (2026-07-21, [ADR
> 0021](../decisions/0021-log-platform-test-infra.md)):** a test-scoped
> OpenSearch domain now realizes this family for the P15 log connector's
> `payments` namespace test source — the first store engine beyond Postgres
> since ADR 0010. Scope is narrow: this is the connector's test pipeline, not
> a migration of `k8fy.events` (which stays Postgres-aliased per ADR 0013)
> off the relational store.

An event with multiple access patterns may land in multiple stores (e.g. a ticket
body → vector for semantic search **and** KV for point-lookup by id). Decide the
redundancy rule in the Principle / an ADR.

## How the two event families fall out (emergent, not hardcoded)

Both of the product's broad families are just different regions of trait-space —
we never name them in the logic:

- **Business / domain events** (order tracking, payments, ticketing; from DBs,
  CRMs, etc.) → mostly `structured + filter/aggregate` → relational; freeform
  parts → vector. Usually `authority: derived` — the DB/CRM is the
  system-of-record, so agentify stores a **queryable mirror/index**, not the master.
- **Operational events** (Kubernetes, cloud signals, audits, dashboards;
  multi-source) → mostly `numeric/metric + time-range-scan` → TSDB, or
  `append-only logs` → log/search index. Often `ephemeral (TTL)` → expire / cold-tier fast.

## Step 3 — The Event Profile (how a new integration plugs in)

Instead of editing this policy, each integration **registers an event profile**.
The storage engine reads the profile → applies the decision function → picks store
+ pod. Profiles are a *starting guess*, refinable by the loop (see below).

```jsonc
{
  "event_namespace": "crm.ticket",        // logical grouping — not a hardcoded type
  "shape": "freeform-text",
  "access_pattern": ["semantic", "point-lookup"],
  "temporality": "append-only",
  "mutability": "immutable",
  "authority": "derived",                 // CRM is the system-of-record
  "source": "salesforce",
  "retention": "365d",
  "pod_hint": "by-namespace"              // or: by-source, by-time-window
}
```

- **Inferred vs declared:** _<does the integration author declare the profile, or
  does the system infer traits from a sample of events? Decide for v1.>_
- **Unknown events:** _<what happens to an event with no profile? default to a
  catch-all pod + flag for classification?>_

## Worked example — K8fy (operational use case)

K8fy integrates Kubernetes signals so operators can ask "is this service/pod
healthy?", "have certs expired / expiring soon?", "should we renew this cert?".
Those questions reveal **three different data natures plus one reasoning step** —
a good illustration of classify-don't-enumerate. The profiles below are the
starting drafts (refinable by the loop).

```jsonc
// 1. Live cluster state — health of a service/pod RIGHT NOW.
// Changes every second and K8s API is the system-of-record, so we DON'T persist
// it; we query live (with a few-seconds cache).
{
  "event_namespace": "k8fy.live-state",
  "shape": "structured",
  "access_pattern": ["point-lookup"],
  "temporality": "current-state",
  "mutability": "mutable",
  "authority": "derived",          // K8s API is the truth
  "source": "kubernetes-api",
  "retention": "ephemeral",        // ~seconds; effectively pass-through
  "pod_hint": "by-namespace"
}

// 2. Certificates — expiry dates. Stored as a small, periodically refreshed
// table because we want to FILTER across all of them ("expiring in 30 days").
{
  "event_namespace": "k8fy.certificates",
  "shape": "structured",
  "access_pattern": ["point-lookup", "filter+aggregate"],
  "temporality": "current-state",
  "mutability": "mutable",         // slow-changing; refreshed on a schedule
  "authority": "derived",
  "source": "kubernetes-api",
  "retention": "until-changed",
  "pod_hint": "by-namespace"
}

// 3. Cluster events — what happened (restarts, failures). K8s events expire in
// ~1h, so we capture them for history. Append-only, searched by time range.
{
  "event_namespace": "k8fy.events",
  "shape": "semi-structured",
  "access_pattern": ["time-range-scan", "filter+aggregate"],
  "temporality": "append-only",
  "mutability": "immutable",
  "authority": "derived",
  "source": "kubernetes-api",
  "retention": "30d",
  "pod_hint": "by-time-window"
}

// 4. Metrics — CPU/mem/restart-counts over time, for trend questions.
{
  "event_namespace": "k8fy.metrics",
  "shape": "numeric/metric",
  "access_pattern": ["time-range-scan"],
  "temporality": "time-series",
  "mutability": "immutable",
  "authority": "derived",
  "source": "metrics-server | prometheus",
  "retention": "90d",
  "pod_hint": "by-namespace"
}
```

Applying the Step-2 decision function gives the pods that **emerge** for K8fy:

| Pod | Store family | Note |
|-----|--------------|------|
| `k8fy.live-state` | thin cache / live pass-through | don't persist — query K8s on demand |
| `k8fy.certificates` | relational / KV (small table) | enables "expiring soon" filters |
| `k8fy.events` | log / search index | history beyond K8s' ~1h retention |
| `k8fy.metrics` | time-series (TSDB) | trends over time |

The "should we renew?" question is **not storage** — it's a judgment: read
`k8fy.certificates` (expiry date) + apply a renewal rule (e.g. renew if < 30 days)
→ answer. See [correlation](correlation.md) and the K8fy spec
[`002-k8fy-health-queries`](../specs/002-k8fy-health-queries.md).

## Cost / efficiency guardrails

- _<storage budget per pod? compress/summarize cold data? dedup? TTL defaults per
  temporality class, e.g. operational logs default to short retention.>_

## How this policy adapts over time

The decision function is fixed; the **profiles are tunable**. The
[refinement-loop](refinement-loop.md) corrects mis-declared profiles from real
usage. Example: a profile claims `semantic`, but >80% of actual queries are
`filter+aggregate` → the loop reclassifies the access pattern and migrates the pod
to a relational store. Cross-ref [pod-formation](pod-formation.md) for the migration mechanics.

## Open questions

- [ ] v1: profiles declared by integration authors, or inferred from event samples?
- [ ] Do we allow the same event in multiple store families (redundancy for speed) vs. single source of truth?
- [ ] For `authority: derived` events, do we store full copies or just indexes/pointers back to the source-of-record?
- [ ] Default retention per temporality class (current-state vs append-only vs time-series)?
- [ ] Minimum viable v1: probably one store family + one catch-all pod, profiles added as integrations land.
