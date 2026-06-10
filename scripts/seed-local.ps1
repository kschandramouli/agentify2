# Seed the local backend with a payment-worker pod + events for testing.
# Run AFTER the backend is up: go run cmd/agentify/main.go
# Usage: .\scripts\seed-local.ps1

$BASE = "http://localhost:8080/api"

Write-Host "Seeding local backend at $BASE..." -ForegroundColor Cyan

# Pods are auto-registered by the ingester when events are ingested — no
# separate registration step needed. Just POST one Event per request.

# Each event matches models.Event:
#   id, timestamp, event_namespace, type, source, entity_key, payload

$now = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

$events = @(
    # Healthy old rollout pods (phase=Succeeded, restarts=0) — trimAgentPayload should drop these
    @{
        id              = [guid]::NewGuid().ToString()
        timestamp       = $now
        event_namespace = "k8fy.live-state"
        type            = "pod_deleted"
        source          = "kubernetes-api"
        entity_key      = "payment-58577fd87c-4dmqq"
        payload         = @{ pod_id = "payment-58577fd87c-4dmqq"; namespace = "payments"; phase = "Succeeded"; ready = $false; restarts = 0 }
    },
    @{
        id              = [guid]::NewGuid().ToString()
        timestamp       = $now
        event_namespace = "k8fy.live-state"
        type            = "pod_deleted"
        source          = "kubernetes-api"
        entity_key      = "payment-58577fd87c-lcjgk"
        payload         = @{ pod_id = "payment-58577fd87c-lcjgk"; namespace = "payments"; phase = "Succeeded"; ready = $false; restarts = 0 }
    },
    @{
        id              = [guid]::NewGuid().ToString()
        timestamp       = $now
        event_namespace = "k8fy.live-state"
        type            = "pod_deleted"
        source          = "kubernetes-api"
        entity_key      = "payment-58577fd87c-mqqr2"
        payload         = @{ pod_id = "payment-58577fd87c-mqqr2"; namespace = "payments"; phase = "Succeeded"; ready = $false; restarts = 0 }
    },
    @{
        id              = [guid]::NewGuid().ToString()
        timestamp       = $now
        event_namespace = "k8fy.live-state"
        type            = "pod_deleted"
        source          = "kubernetes-api"
        entity_key      = "payment-756b76dfb6-t6flz"
        payload         = @{ pod_id = "payment-756b76dfb6-t6flz"; namespace = "payments"; phase = "Succeeded"; ready = $false; restarts = 0 }
    },
    @{
        id              = [guid]::NewGuid().ToString()
        timestamp       = $now
        event_namespace = "k8fy.live-state"
        type            = "pod_deleted"
        source          = "kubernetes-api"
        entity_key      = "payment-756b76dfb6-kc845"
        payload         = @{ pod_id = "payment-756b76dfb6-kc845"; namespace = "payments"; phase = "Succeeded"; ready = $false; restarts = 0 }
    },
    # Service record (should be kept)
    @{
        id              = [guid]::NewGuid().ToString()
        timestamp       = $now
        event_namespace = "k8fy.live-state"
        type            = "service_added"
        source          = "kubernetes-api"
        entity_key      = "payment"
        payload         = @{ service = "payment"; namespace = "payments"; cluster_ip = "172.20.218.167"; ports = 1 }
    },
    # Active crashing workers (should be KEPT by trimAgentPayload)
    @{
        id              = [guid]::NewGuid().ToString()
        timestamp       = $now
        event_namespace = "k8fy.live-state"
        type            = "pod_modified"
        source          = "kubernetes-api"
        entity_key      = "payment-worker-68795899ff-kngf7"
        payload         = @{ pod_id = "payment-worker-68795899ff-kngf7"; namespace = "payments"; phase = "Running"; ready = $false; restarts = 112 }
    },
    @{
        id              = [guid]::NewGuid().ToString()
        timestamp       = $now
        event_namespace = "k8fy.live-state"
        type            = "pod_modified"
        source          = "kubernetes-api"
        entity_key      = "payment-worker-68795899ff-zcml4"
        payload         = @{ pod_id = "payment-worker-68795899ff-zcml4"; namespace = "payments"; phase = "Running"; ready = $false; restarts = 24 }
    }
)

$ok = 0
$fail = 0
foreach ($ev in $events) {
    $body = $ev | ConvertTo-Json -Depth 5
    try {
        $null = Invoke-RestMethod -Uri "$BASE/ingest" -Method Post `
            -ContentType "application/json" -Body $body -ErrorAction Stop
        Write-Host "  ingested: $($ev.entity_key) ($($ev.type))" -ForegroundColor Green
        $ok++
    } catch {
        Write-Host "  FAILED:   $($ev.entity_key) — $($_.Exception.Message)" -ForegroundColor Red
        $fail++
    }
}

Write-Host ""
Write-Host "Done: $ok ingested, $fail failed." -ForegroundColor Cyan
Write-Host ""
Write-Host "Expected after trim: 3 events kept (service + 2 crashing workers)." -ForegroundColor Yellow
Write-Host ""
Write-Host "Test queries:" -ForegroundColor Cyan
Write-Host '  curl http://localhost:8080/health'
Write-Host '  curl -s -X POST http://localhost:8080/api/query -H "Content-Type: application/json" -d "{\"question\":\"why is payment-worker degraded?\",\"context\":{\"namespace\":\"payments\"}}" | python -m json.tool'
