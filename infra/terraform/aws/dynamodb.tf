# ── DynamoDB: pod registry ────────────────────────────────────────────────────
# Moved from infra/aws/dynamodb_pod_registry.tf and integrated into the full
# module. The table itself is unchanged; the store_type GSI that was previously
# defined (but lacks a hash_key attribute declaration) is omitted — DynamoDB
# requires every GSI key attribute to be declared in the top-level `attribute`
# blocks, and store_type was missing. Add it back when needed.

resource "aws_dynamodb_table" "pod_registry" {
  name         = "${var.project}-pod-registry"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }

  attribute {
    name = "namespace"
    type = "S"
  }

  attribute {
    name = "lifecycle"
    type = "S"
  }

  global_secondary_index {
    name            = "namespace-lifecycle-index"
    hash_key        = "namespace"
    range_key       = "lifecycle"
    projection_type = "ALL"
  }

  point_in_time_recovery {
    enabled = true
  }
}
