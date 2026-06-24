"""K8fy eval harness — runs the k8fy-regression Langfuse dataset against a
deployed agentify backend and fails if the mean score falls below the gate.

Usage (local):
    export LANGFUSE_PUBLIC_KEY=pk-lf-...
    export LANGFUSE_SECRET_KEY=sk-lf-...
    export LANGFUSE_BASE_URL=https://us.cloud.langfuse.com
    export BACKEND_URL=http://localhost:8080

    python scripts/run_evals.py

Usage (CI — called from deploy.yml after rollout):
    python scripts/run_evals.py \\
        --backend-url http://localhost:18080 \\
        --run-name ci-$IMAGE_TAG \\
        --pass-threshold 0.85

Exit codes:
    0  All items scored at or above the pass threshold
    1  Mean score below threshold OR eval run itself errored
"""

import argparse
import os
import sys
import time
from typing import Any

import requests
from langfuse import Langfuse

DATASET_NAME   = "k8fy-regression"
SCORE_NAME     = "eval-regression"
DEFAULT_THRESHOLD = 0.85


# ---------------------------------------------------------------------------
# Scoring logic
# ---------------------------------------------------------------------------

def _required_detail_present(details: dict, key: str) -> bool:
    """Return True when details[key] is present and non-empty."""
    val = details.get(key)
    if val is None:
        return False
    if isinstance(val, (list, str)):
        return len(val) > 0
    return True


def score_response(
    response: dict,
    expected: dict,
    latency_ms: float,
) -> tuple[float, str]:
    """Score a single response against expected ground truth.

    Returns (score 0.0–1.0, human-readable detail string).

    Penalty breakdown (penalties stack, floor is 0.0):
      -0.40  wrong intent    (routing failure — most severe)
      -0.25  wrong tier      (tier1/tier2 mismatch)
      -0.20  wrong status    (status not in expected list, when list non-empty)
      -0.05  each missing required_details field (max 4 fields → max -0.20)
      -0.10  latency exceeded (when latency_ms_max > 0)
    """
    score  = 1.0
    issues = []

    # Intent check
    got_intent = response.get("intent", "")
    exp_intent = expected.get("intent", "")
    if exp_intent and got_intent != exp_intent:
        score -= 0.40
        issues.append(f"intent={got_intent!r} (want {exp_intent!r})")

    # Tier check
    got_tier = response.get("tier", "")
    exp_tier = expected.get("tier", "")
    if exp_tier and got_tier != exp_tier:
        score -= 0.25
        issues.append(f"tier={got_tier!r} (want {exp_tier!r})")

    # Status check
    exp_statuses = expected.get("status", [])
    got_status   = response.get("status", "")
    if exp_statuses and got_status not in exp_statuses:
        score -= 0.20
        issues.append(f"status={got_status!r} (want one of {exp_statuses})")

    # Required detail fields
    details = response.get("details") or {}
    for field in expected.get("required_details", []):
        if not _required_detail_present(details, field):
            score -= 0.05
            issues.append(f"details.{field} missing or empty")

    # Latency gate
    max_ms = expected.get("latency_ms_max", 0)
    if max_ms > 0 and latency_ms > max_ms:
        score -= 0.10
        issues.append(f"latency={latency_ms:.0f}ms > {max_ms}ms")

    score = max(0.0, round(score, 4))
    detail_str = "; ".join(issues) if issues else "all checks passed"
    return score, detail_str


# ---------------------------------------------------------------------------
# Main eval loop
# ---------------------------------------------------------------------------

def run_evals(backend_url: str, run_name: str, pass_threshold: float) -> bool:
    """Run the full dataset and return True if the gate passes."""
    public_key = os.environ.get("LANGFUSE_PUBLIC_KEY", "")
    secret_key = os.environ.get("LANGFUSE_SECRET_KEY", "")
    base_url   = os.environ.get("LANGFUSE_BASE_URL", "https://us.cloud.langfuse.com")
    if not public_key or not secret_key:
        print(
            "ERROR: LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY must be set.",
            file=sys.stderr,
        )
        return False

    lf = Langfuse(public_key=public_key, secret_key=secret_key, base_url=base_url)

    try:
        dataset = lf.get_dataset(DATASET_NAME)
    except Exception as exc:
        print(f"ERROR: could not fetch dataset '{DATASET_NAME}': {exc}", file=sys.stderr)
        print("       Run scripts/seed_eval_dataset.py first.", file=sys.stderr)
        return False

    items = list(dataset.items)
    if not items:
        print(f"ERROR: dataset '{DATASET_NAME}' has no items.", file=sys.stderr)
        return False

    print(f"Running {len(items)} eval items against {backend_url}")
    print(f"Run name : {run_name}")
    print(f"Gate     : mean score ≥ {pass_threshold}\n")

    scores: list[float] = []
    session = requests.Session()
    session.headers["Content-Type"] = "application/json"

    for item in items:
        item_input: dict[str, Any] = item.input or {}
        expected: dict[str, Any]   = item.expected_output or {}
        item_id: str               = item.id or "unknown"

        # ------------------------------------------------------------------
        # 1. Create a Langfuse trace for this eval run item.
        #    We create one trace per item so Langfuse groups them as a
        #    "run" of the dataset that appears in the Experiments view.
        # ------------------------------------------------------------------
        trace = lf.trace(
            name=f"eval.{item_id}",
            input=item_input,
            session_id=run_name,
            tags=["eval", "ci"],
        )

        # ------------------------------------------------------------------
        # 2. Call agentify
        # ------------------------------------------------------------------
        query_body = {
            "question": item_input.get("question", ""),
            "context":  item_input.get("context", {}),
        }
        t0 = time.time()
        try:
            http_resp = session.post(
                f"{backend_url}/api/query",
                json=query_body,
                timeout=30,
            )
            http_resp.raise_for_status()
            result = http_resp.json()
        except Exception as exc:
            latency_ms = (time.time() - t0) * 1000
            result = {"error": str(exc), "status": "error", "answer": ""}
            print(f"  FAIL {item_id}  HTTP error: {exc}")

        latency_ms = (time.time() - t0) * 1000

        # ------------------------------------------------------------------
        # 3. Score
        # ------------------------------------------------------------------
        score_val, detail = score_response(result, expected, latency_ms)
        scores.append(score_val)

        # ------------------------------------------------------------------
        # 4. Update trace output and link to the dataset item
        # ------------------------------------------------------------------
        trace.update(
            output=result,
            metadata={
                "agentify_trace_id": result.get("trace_id", ""),
                "score": score_val,
                "detail": detail,
                "latency_ms": round(latency_ms),
            },
        )

        # Link this trace to the dataset item — creates an experiment "run"
        # visible in Langfuse UI under Datasets → k8fy-regression → Runs
        item.link(trace=trace, run_name=run_name)

        # ------------------------------------------------------------------
        # 5. Attach score to the Langfuse trace
        # ------------------------------------------------------------------
        lf.create_score(
            name=SCORE_NAME,
            value=score_val,
            trace_id=trace.id,
            data_type="NUMERIC",
            comment=f"[{item_id}] {detail} | latency={latency_ms:.0f}ms",
        )

        symbol = "✓" if score_val >= pass_threshold else "✗"
        print(
            f"  {symbol}  {score_val:.2f}  {item_id:<45}"
            f"  {detail}  ({latency_ms:.0f}ms)"
        )

    # Flush to ensure all scores reach Langfuse before CI exits
    lf.flush()

    mean = sum(scores) / len(scores) if scores else 0.0
    passed = mean >= pass_threshold
    symbol = "✓ PASS" if passed else "✗ FAIL"

    print(f"\n{'─' * 72}")
    print(f"  {symbol}  mean={mean:.3f}  threshold={pass_threshold}")
    print(f"  {len([s for s in scores if s >= pass_threshold])}/{len(scores)} items at or above threshold")
    print(f"  Langfuse run: {run_name}")
    print(f"{'─' * 72}")

    return passed


def main() -> None:
    parser = argparse.ArgumentParser(description="K8fy eval harness")
    parser.add_argument(
        "--backend-url",
        default=os.environ.get("BACKEND_URL", "http://localhost:8080"),
        help="Base URL of the agentify backend (default: http://localhost:8080)",
    )
    parser.add_argument(
        "--run-name",
        default=os.environ.get("IMAGE_TAG", f"local-{int(time.time())}"),
        help="Name for this eval run in Langfuse (default: IMAGE_TAG env var)",
    )
    parser.add_argument(
        "--pass-threshold",
        type=float,
        default=float(os.environ.get("EVAL_PASS_THRESHOLD", DEFAULT_THRESHOLD)),
        help=f"Minimum mean score to pass (default: {DEFAULT_THRESHOLD})",
    )
    args = parser.parse_args()

    passed = run_evals(
        backend_url=args.backend_url,
        run_name=args.run_name,
        pass_threshold=args.pass_threshold,
    )
    sys.exit(0 if passed else 1)


if __name__ == "__main__":
    main()
