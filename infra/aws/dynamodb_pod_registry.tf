# DynamoDB table for pod registry

resource "aws_dynamodb_table" "pod_registry" {
  name           = "agentify-pod-registry"
  billing_mode   = "PAY_PER_REQUEST"  # On-demand pricing (scales automatically)
  hash_key       = "id"               # Partition key

  attribute {
    name = "id"
    type = "S"  # String
  }

  attribute {
    name = "namespace"
    type = "S"
  }

  attribute {
    name = "lifecycle"
    type = "S"
  }

  # Global Secondary Indexes for querying
  global_secondary_index {
    name            = "namespace-lifecycle-index"
    hash_key        = "namespace"
    range_key       = "lifecycle"
    projection_type = "ALL"
  }

  global_secondary_index {
    name            = "store-type-index"
    hash_key        = "store_type"
    projection_type = "ALL"
  }

  # Point-in-time recovery for data protection
  point_in_time_recovery {
    enabled = true
  }

  # TTL for automatic cleanup of retired pods (optional)
  # ttl {
  #   attribute_name = "ttl"
  #   enabled        = true
  # }

  tags = {
    Application = "agentify"
    Component   = "pod-registry"
  }
}

output "pod_registry_table_name" {
  value = aws_dynamodb_table.pod_registry.name
}

output "pod_registry_table_arn" {
  value = aws_dynamodb_table.pod_registry.arn
}
