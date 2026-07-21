# EKS Fargate's built-in log router (Fargate Fluent Bit) reads its config from
# this ConfigMap — no Fluent Bit DaemonSet to deploy or manage; AWS injects
# the router as a managed sidecar per pod once this exists (ADR 0021).
#
# Applied via scripts/onboard_cluster_logging.sh, not Terraform's kubernetes
# provider — see ADR 0021 for why (provider connections are static in HCL,
# can't loop across multiple clusters' API servers the way ordinary
# for_each'd resources can).
#
# Placeholders (${...}) are substituted by the onboarding script from the
# `clusters`/`opensearch` Terraform outputs — nothing here should be hand-edited
# per cluster; edit the template once, re-run the script per cluster.
#
# NOTE: verify this against AWS's current Fargate logging documentation before
# relying on it in a new region/EKS version — the Fargate Fluent Bit image's
# supported OUTPUT plugin options have changed across EKS platform versions.
apiVersion: v1
kind: Namespace
metadata:
  name: aws-observability
  labels:
    aws-observability: "true"
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: aws-logging
  namespace: aws-observability
data:
  filters.conf: |
    [FILTER]
        Name                kubernetes
        Match               kube.*
        Merge_Log           On
        Buffer_Size         0
        Kube_Meta_Cache_TTL 300s
  output.conf: |
    [OUTPUT]
        Name                firehose
        Match               *
        region              ${aws_region}
        delivery_stream     ${firehose_stream_name}
