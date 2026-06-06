# Glossary

> Keep domain + architecture terms here so naming stays consistent across specs,
> code, and conversations with Claude. Add a line whenever a new term appears.

| Term | Definition |
|------|------------|
| **Context-mesh** | The overall architecture: a network of independent pods queried and correlated by an orchestrator. Two layers — *design-time* (this repo) and *runtime* (live data). |
| **Pod** | A self-contained unit of stored data owning a coherent slice, in whatever store type best fits it. Emergent — created/refined by the system, not predefined. |
| **Orchestrator** | The "primary pod" / front door: interprets a query, routes to pod(s), correlates results, returns the answer. |
| **Pod-registry** | The runtime, self-maintained map of all pods (what each owns, freshness, stats, lifecycle). The orchestrator's source of truth for routing. |
| **Policy** | An English-first rule set governing behavior. The four: storage-strategy, pod-formation, refinement-loop, correlation. |
| **Refinement loop** | The feedback cycle that reshapes storage/routing based on streamed events and query outcomes. |
| **Event** | A unit of incoming data streamed into the mesh and stored in a pod. |
| **Correlation** | Combining results from multiple pods into one coherent answer. |
| _<your domain term>_ | _<definition>_ |
