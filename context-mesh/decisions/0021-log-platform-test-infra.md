# 0021 – Test log-platform infra for P15 (Fargate + Firehose + OpenSearch)

## Status

Accepted   ·   (date: 2026-07-21)

## Context

P15 ([ROADMAP.md](../ROADMAP.md)) designed a pull-based OpenSearch log
connector for the agent, replacing direct-cluster log fetch as the intended
production shape — but there was nowhere to test it against. The only log
source in the system was on-demand K8s pod-log fetch (spec 008, ADR 0014),
which is deliberately ephemeral and never persisted.

We needed a real, separately-isolated log source and a real ingest pipeline
into OpenSearch to validate P15's connector against, without:
- provisioning a second EKS cluster (a second $73/mo control plane, a second
  VPC/ALB/Vault setup, and a second OIDC trust-policy configuration — none of
  which is needed just to prove out a log connector), or
- committing to expensive, always-on infrastructure for what is fundamentally
  test/demo tooling, not production load.

This is also the **first storage backend introduced since ADR 0010** collapsed
everything to a single Postgres store — `storage-strategy.md`'s `log / search
index` trait family has existed in the decision function since day one, but
was only ever realized by aliasing to the Postgres `events` table (ADR 0013).
This ADR is the first case where it's realized by a real log/search engine.

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
2. **Ingest via Kinesis Firehose, not straight to OpenSearch.** Slightly more
   moving parts than a direct write, but leaves room for a second destination
   (S3 cold-archive) later without touching the ingest path again, and
   matches the org's stated intent to standardize on Firehose as the fan-out
   layer.
3. **OpenSearch domain: VPC-based, IAM-authenticated, single instance, no
   dedicated master.** Matches P15's IAM/IRSA auth decision; avoids the
   internal fine-grained-access-control user database (unneeded complexity
   for this). This is the dominant cost line item of the whole design —
   estimate, don't assume, via the AWS Pricing Calculator before leaving it
   running unattended.
4. **Multi-cluster onboarding is registry + `for_each`, not a Terraform
   module per cluster.** A Fargate profile is a pure EKS/AWS-API resource
   (`aws_eks_fargate_profile`) — it needs no live connection to the target
   cluster's Kubernetes API, so every cluster's profile + IAM permissions are
   driven by one `for_each` over a `variable "clusters"` map. Onboarding a
   new cluster is one map entry, not new HCL. The **one piece that can't work
   this way** is the `aws-observability` ConfigMap itself: `kubernetes`/`helm`
   provider *connections* are static in HCL, and Terraform has no supported
   way to loop a provider connection across N clusters' API servers the way
   ordinary resources loop via `for_each`. That one object is applied via a
   thin script (`scripts/onboard_cluster_logging.sh`) reading the same
   `clusters` map via `terraform output -json`, kept consistent across every
   cluster (including the first) rather than special-casing cluster #1.
5. **Explicit cost toggle.** `variable "enable_log_platform_test"` (default
   `false`) gates the entire Fargate-profile/Firehose/OpenSearch block. The
   dominant cost item only exists when a test session is actually running;
   tearing it down between sessions requires no hand-tracking of which
   resources to destroy.
6. **The actual `OpenSearchLogSource` Go connector (`LogSource` interface,
   query construction — already designed in the P15 roadmap entry) is
   explicitly out of scope here.** This ADR covers only the infra + ingest
   pipeline needed to produce real, queryable log data to build that
   connector against.

## Consequences

- **Positive:** a real, low-cost, isolated log source to validate P15
  against; a clean, low-friction path to onboard additional clusters later
  (P16) without rearchitecting; the Firehose/OpenSearch pipeline is shared/
  centralized across every onboarded cluster rather than duplicated per
  cluster.
- **Negative / cost accepted:** the OpenSearch domain is the first
  continuously-billed resource outside the RDS/EKS baseline this project
  already carries — worth monitoring via Cost Explorer, not just trusting the
  toggle exists. Firehose-to-VPC-OpenSearch delivery and Fargate-in-public-
  subnets connectivity are both AWS features assumed to behave the same way
  the existing EC2 node group's public-subnet/no-NAT setup does — flagged in
  the implementation as "validate, don't assume" rather than asserted with
  certainty.
- **Negative / cost accepted:** the onboarding script (not pure Terraform)
  for the one K8s-native artifact is a deliberate compromise forced by a real
  Terraform limitation (static provider connections), not a preference —
  revisit if Terraform ever adds first-class multi-cluster provider
  iteration.
- **Revisit if:** OpenSearch/Firehose costs materially exceed the rough
  estimate once real usage is measured; a second cluster is actually
  onboarded (exercises the `for_each` design for the first time); or the P15
  connector, once built, reveals the greenfield schema needs revision.
