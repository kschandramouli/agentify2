# 0021 – Test log-platform infra for P15 (Fargate + Firehose + S3/Athena)

## Status

Accepted   ·   (date: 2026-07-21   ·   revised: 2026-07-22 — OpenSearch destination
replaced with S3 + Athena; revised again 2026-07-25 — test harness graduates to
a first-class production log source; revised again 2026-07-27 — Splunk/
Elasticsearch/OpenSearch reframed as additional connector options alongside
Athena, not a replacement priority above it; revised again 2026-07-27 —
generalized from one hardcoded namespace to a list of onboarded namespaces per
cluster, all sharing one common Glue database/table (renamed `payments_logs`
→ `pod_logs`); see "Revision" sections below)

## Context

P15 ([ROADMAP.md](../ROADMAP.md)) designed a pull-based log connector for the
agent — reading whatever log platform is already the customer's source of
truth (most commonly Splunk, Elasticsearch, or OpenSearch), rather than
hitting cluster logs directly. But there was nowhere to test that connector
against. The only log source in the system was on-demand K8s pod-log fetch
(spec 008, ADR 0014), which is deliberately ephemeral and never persisted.

We needed a real, separately-isolated log source and a real ingest pipeline
to validate P15's connector against, without:
- provisioning a second EKS cluster (a second $73/mo control plane, a second
  VPC/ALB/Vault setup, and a second OIDC trust-policy configuration — none of
  which is needed just to prove out a log connector), or
- committing to expensive, always-on infrastructure for what is fundamentally
  test/demo tooling, not production load.

This is also the **first storage backend introduced since ADR 0010** collapsed
everything to a single Postgres store — `storage-strategy.md`'s `log / search
index` trait family has existed in the decision function since day one, but
was only ever realized by aliasing to the Postgres `events` table (ADR 0013).
This ADR is the first case where it's realized by something else.

## Important distinction: this ADR is a test harness, not the production connector

*(Original 2026-07-21 framing — superseded by the 2026-07-25 and 2026-07-27
revisions below, which promote this harness from test-only scaffolding to a
first-class, always-available data source. Kept here for the historical
reasoning; read the revisions for the current decision.)*

In production, agentify never owns the log-ingest pipeline — the customer's
own logging pipeline (their Fluent Bit/Logstash/vendor agents) already feeds
their existing Splunk/Elasticsearch/OpenSearch, and P15's job is only to
**read** from that platform at diagnosis time. Everything Fargate/Firehose/S3
related in this ADR is scaffolding **we** stand up, purely to generate
realistic, queryable test data to build and validate the read connector
against — it is not what ships to a customer.

**Connector lineup (reframed 2026-07-27 — see the 2026-07-27 revision below):**
Athena/Glue is the first live data source (shipped 2026-07-25). Splunk and
Elasticsearch/OpenSearch are **additional** connectors to broaden integration
options — for customers whose own log platform is already Splunk or an ES/
OpenSearch index — not a replacement for Athena. Splunk is its own
implementation (SPL via the REST search-jobs API, Splunk token auth).
Elasticsearch and OpenSearch share the same `_search` Query DSL closely
enough that one connector implementation covers both. All three sit behind
the same `LogSource` interface already designed in the P15 roadmap entry.

## Decision

1. **Compute isolation: EKS Fargate profile, not a second cluster or a
   scale-to-zero EC2 node group.** Fargate has zero idle cost (pay per
   pod-second only) and needs no Fluent Bit DaemonSet — EKS Fargate has a
   built-in log router (configured via a single ConfigMap in the
   `aws-observability` namespace) that ships pod stdout/stderr to CloudWatch
   Logs, Kinesis Firehose, or OpenSearch directly, with no extra pods to run.
   Confirmed the `payments` test workloads (`payment-api`/`payment-worker`/
   `payment-service`) are Fargate-safe: they use a plain initContainer
   (curl+jq against Vault's HTTP API) for cert issuance, not Vault Agent
   Injector sidecar/webhook injection, and have no DaemonSets, `hostPath`,
   `privileged`, or `hostNetwork` usage.
2. **Ingest via Kinesis Firehose.** Firehose is one-way delivery only (no read
   API) — its role here is purely test-harness plumbing to get Fargate's
   stdout into a queryable place cheaply, and it isn't tied to any one
   destination: the same stream can be repointed or fanned out later without
   touching the Fargate/ingest side.
3. **Destination: S3 (Hive-partitioned by hour) + Athena, not an OpenSearch
   domain.** Revised 2026-07-22 — see "Revision" below.
4. **Multi-cluster (and, since 2026-07-27, multi-namespace) onboarding is
   registry + `for_each`, not a Terraform module per cluster or per
   namespace.** A Fargate profile is a pure EKS/AWS-API resource
   (`aws_eks_fargate_profile`) — it needs no live connection to the target
   cluster's Kubernetes API, so every (cluster, namespace) pair's profile +
   IAM permissions are driven by one `for_each` over a flattened
   `variable "clusters"` map (each cluster now lists `namespaces`, not a
   single `namespace`). Onboarding a new cluster, or a new namespace/service
   on an existing cluster, is one map entry, not new HCL. The **one piece
   that can't work
   this way** is the `aws-observability` ConfigMap itself: `kubernetes`/`helm`
   provider *connections* are static in HCL, and Terraform has no supported
   way to loop a provider connection across N clusters' API servers the way
   ordinary resources loop via `for_each`. That one object is applied via a
   thin script (`scripts/onboard_cluster_logging.sh`) reading the same
   `clusters` map via `terraform output -json`, kept consistent across every
   cluster (including the first) rather than special-casing cluster #1.
5. **Explicit cost toggle.** `variable "enable_log_platform_test"` (default
   `false`) gates the entire Fargate-profile/Firehose/S3/Athena block.
   Tearing it down between test sessions requires no hand-tracking of which
   resources to destroy.
6. **The actual production `LogSource` connector implementations (Splunk,
   Elasticsearch/OpenSearch) are explicitly out of scope here.** This ADR
   covers only the infra + ingest pipeline needed to produce real, queryable
   test log data — not the connector code itself, and not a customer-facing
   deliverable.

## Revision (2026-07-22): OpenSearch domain replaced with S3 + Athena

The original decision used a VPC-based OpenSearch domain as the Firehose
destination. Reconsidered because:
- **It doesn't match what's being tested.** Real customers' source of truth
  is Splunk or Elasticsearch/OpenSearch (their own, already populated by
  their own pipeline) — this test harness was never meant to *be* that
  production connector target, so there's no reason it needs to run a real
  search-engine instance.
- **Cost and setup complexity.** The OpenSearch domain was the dominant,
  continuously-billed cost item and needed a VPC, security groups for both
  querying and Firehose's VPC-delivery ENIs, and an IAM access-policy design
  — all removed by switching destinations.
- **Athena has zero idle cost** (pay-per-query-scanned-bytes only, no
  standing instance) and still satisfies the pull/on-demand, bounded-time-
  window query discipline P15 is built around — via Hive-style
  `year=/month=/day=/hour=` S3 partitioning and Athena **partition
  projection** (computed from the query's time range, not a Glue crawler or
  `MSCK REPAIR TABLE` sync step).
- Firehose's destination changed from `opensearch_configuration` to
  `extended_s3_configuration` — no VPC config needed at all for an S3
  destination (unlike OpenSearch, which required VPC-delivery ENIs).
- Query access reuses the same IRSA roles as before
  (`module.backend_irsa`/`module.agent_irsa`) — extended with
  Athena/Glue/S3 read permissions instead of an OpenSearch `es:*` access
  policy statement.

## Revision (2026-07-25): test harness graduates to an interim production log source

**Reconsidered.** The agent-side connector (`src/agent/k8fy/log_router.py`,
`log_platform.py`) was built to validate the read-connector discipline
against this harness — and, once built and validated, there was no reason to
gate it behind "test only" any further. `log_router.get_logs()` now tries
this harness first for every namespace whenever it's configured
(`ATHENA_WORKGROUP`/`DATABASE`/`TABLE` on the agent pod), falling back to the
live cluster on empty results or errors — no per-namespace registry, no
manual toggle. This is genuinely in production code paths now: the chat
tool-calling loop and `DiagnoseSkill`'s deterministic prefetch both call it.

**Also corrects a stale claim from the original ADR:** the Glue table
schema described below as "not yet validated against a real ingested
record" *was* validated this session — the real Fargate/Fluent-Bit output
turned out not to match the original assumption (`log` is a plain string,
the raw CRI log line, not `struct<level:string>`; no top-level `@timestamp`/
`message`/`stream` keys exist; `kubernetes.labels`/`annotations` need
`map<string,string>`, not fixed structs). Fixed directly against a
downloaded, inspected S3 object and confirmed live via real Athena query
results.

## Revision (2026-07-27): Splunk/Elasticsearch/OpenSearch reframed as additional connectors, not a replacement priority

**Reconsidered again.** The 2026-07-25 revision still framed Athena as a
stopgap "filling the gap" until "the real Splunk/ES connector is built" —
implying Splunk/ES would eventually supersede it. That hierarchy is dropped.
Athena/Glue is now one of potentially several **peer** data sources behind
the `LogSource` abstraction: it's shipped, validated against real data, and
zero idle cost — there's no reason to treat it as disposable once a second
connector exists.

**Splunk and Elasticsearch/OpenSearch remain valuable, but as *additional*
integration options**, not the priority Athena is measured against. They
matter for a specific case Athena doesn't cover — a customer whose logs
already live in their own, already-populated Splunk/ES/OpenSearch instance,
where agentify should read from that existing platform rather than ask them
to stand up a second pipeline. Building them **broadens** which log
platforms agentify can plug into; it doesn't **replace** the Athena path.
The goal going forward: a flexible, multi-connector `get_logs()` that can be
configured toward whichever platform(s) a given deployment actually has —
Athena, Splunk, ES/OpenSearch, or more than one at once — rather than a
single hardcoded destination.

This makes the Go-side `LogSource` interface (see "consequences" below) more
valuable, not less: with Athena as a peer connector rather than a stopgap,
a real pluggable abstraction (vs. one bespoke agent-side function per
backend) is worth doing sooner rather than only "when Splunk/ES land."

## Revision (2026-07-27): generalized to multiple namespaces/services per cluster

**Reconsidered again.** The pipeline shipped onboarding exactly one namespace
(`payments`) per cluster — `var.clusters`' `namespace` field was a single
string, and the Fargate profile's selector was tied to it directly. That
was an accident of "only one test workload existed yet," not an intentional
constraint, and it doesn't match the "common Glue database/table shared
across services" framing above: the Firehose → S3 → Glue destination was
already architecturally shared (one bucket, one database, one table, `count`
not `for_each`); only the *namespace onboarding* step was artificially
single-namespace.

**Generalized:** `var.clusters`' `namespace: string` field is now
`namespaces: list(string)`. `local.log_platform_cluster_namespaces` flattens
every (cluster, namespace) pair from that list into its own map entry, and
`aws_eks_fargate_profile.log_test` now does `for_each` over that flattened
map — one Fargate profile per onboarded namespace, each with its own
selector, all still feeding the single shared Firehose/S3/Athena/Glue
resources below (still `count`, not `for_each` — there is exactly one
pipeline, not one per namespace). Onboarding an additional service on an
already-onboarded cluster is now "add a namespace to that cluster's list,"
not new pipeline infrastructure.

**Also renamed the Glue table** from `payments_logs` to `pod_logs`
(`local.glue_table_name`) — the old name implied a payments-specific table
when the table has always held (and now explicitly is designed to hold)
every onboarded namespace's logs, distinguished per-row by the
`kubernetes.namespace_name`/`pod_name` columns, not by table name. Backend
config (`infra/kubernetes/agent.yaml`'s `ATHENA_TABLE`) updated to match.

**Not yet applied live** — this is a Terraform code change only. Applying it
will force-replace the existing `payments` Fargate profile, since EKS
Fargate profile selectors are immutable (any selector change requires
destroy + recreate, not an in-place update). Plan carefully before applying
against the live `agentify-dev` cluster.

## Consequences

- **Positive:** a real, near-zero-cost, isolated log source to validate P15's
  connector interface against; a clean, low-friction path to onboard
  additional clusters later (P16) without rearchitecting; the Firehose/S3/
  Athena pipeline is shared/centralized across every onboarded cluster
  rather than duplicated per cluster; as of the 2026-07-25 revision, a real
  first-class log source for diagnosis rather than pure test scaffolding; as
  of the 2026-07-27 revision, the first of what's meant to be several peer
  connectors, not a placeholder that Splunk/ES are expected to replace.
- **Negative / architecture debt accepted (2026-07-25, sharpened
  2026-07-27):** the routing decision lives in the Python agent
  (`log_router.py`, calling Athena directly via boto3), not behind the Go
  `LogSource` interface P15 actually designed (`internal/api/adapter_client.go`,
  `LOG_SOURCE=k8s_adapter|opensearch` config selection). Acceptable
  short-term — it works, it's tested, it's isolated to one function — but
  now that Athena is a peer connector rather than a stopgap, adding Splunk
  or Elasticsearch/OpenSearch as a second bespoke agent-side function instead
  of building the real pluggable interface would compound this debt rather
  than pay it down; the interface is worth building before (or as part of)
  whichever connector comes next.
- **Negative / cost accepted:** Fargate-in-public-subnets connectivity
  (reaching ECR/Firehose without NAT) is assumed to behave like the existing
  EC2 node group's public-subnet setup, not yet confirmed live.
- **Negative / cost accepted:** the onboarding script (not pure Terraform)
  for the one K8s-native artifact is a deliberate compromise forced by a real
  Terraform limitation (static provider connections), not a preference —
  revisit if Terraform ever adds first-class multi-cluster provider
  iteration.
- **Revisit if:** Athena query costs or S3 storage materially exceed
  expectations once real usage is measured; a second cluster is actually
  onboarded (exercises the `for_each` design for the first time); or building
  the real Splunk/Elasticsearch connectors reveals the greenfield schema
  needs revision, or makes it worth moving this Athena path behind the same
  Go-side `LogSource` interface instead of leaving it as a separate
  agent-side connector.
