# Node Lifecycle Manager

For a quick way to run it locally, see [README.run-local.md](README.run-local.md).

Automates node lifecycle management in AKS clusters. Monitors node health, drains bad nodes, scales your cluster, and sends alerts when things go wrong.

## What It Does

Runs every 60 seconds (configurable) and:

1. Health checks all nodes (ready status, disk/memory/PID pressure, kubelet)
2. Cordons and drains nodes that fail checks repeatedly
3. Sends Slack or webhook alerts
4. Scales node pools based on Azure Monitor metrics (optional)
5. Exposes Prometheus metrics

## Project Structure

```
├── cmd/
│   └── controller/
│       └── main.go              # Entry point, wires everything together
├── pkg/
│   ├── alerting/
│   │   └── alerter.go           # Slack and webhook notifications
│   ├── azure/
│   │   └── client.go            # Azure SDK wrapper for AKS and Monitor APIs
│   ├── config/
│   │   └── config.go            # Configuration loading and defaults
│   ├── controller/
│   │   └── controller.go        # Main reconciliation loop
│   ├── drain/
│   │   └── drainer.go           # Node cordon/drain/uncordon operations
│   ├── health/
│   │   └── checker.go           # Pluggable health check system
│   └── metrics/
│       └── collector.go         # Prometheus metrics
├── deploy/
│   ├── rbac.yaml                # ServiceAccount, ClusterRole, ClusterRoleBinding
│   ├── configmap.yaml           # Controller configuration
│   └── deployment.yaml          # Deployment and Service
├── dashboards/
│   └── grafana.json             # Grafana dashboard for monitoring
├── config.example.yaml          # Example configuration file
├── Dockerfile                   # Multi-stage build
├── Makefile                     # Build and deploy commands
└── go.mod                       # Go module definition
```

## How It Works

### Reconciliation Loop

The controller runs a reconciliation loop on a configurable interval (default 60s):

```
For each node in the cluster:
  1. Run all configured health checks
  2. If healthy, reset the unhealthy counter
  3. If unhealthy, increment the counter
  4. If counter >= threshold (default 3), trigger remediation
```

### Health Checks

Health checks are pluggable. Currently implemented:

| Check | What it does |
|-------|--------------|
| `node-condition` | Checks if node Ready condition is True and heartbeat is recent |
| `disk-pressure` | Checks DiskPressure condition |
| `memory-pressure` | Checks MemoryPressure condition |
| `pid-pressure` | Checks PIDPressure condition |
| `kubelet` | Hits kubelet /healthz endpoint |
| `network` | Checks NetworkUnavailable condition |

### Remediation Flow

When a node is determined unhealthy:

1. Send alert notification
2. Cordon the node (mark unschedulable)
3. Drain pods from the node (respects PodDisruptionBudgets)
4. Send completion notification
5. If autoscaling enabled, request node replacement via Azure API

### Autoscaling

When enabled, the controller queries Azure Monitor for cluster CPU and memory utilization:

- If utilization > `scaleUpThreshold` (default 80%), add a node
- If utilization < `scaleDownThreshold` (default 30%), remove a node
- Cooldown periods prevent rapid scaling

## Configuration

Copy the example config:

**bash:**
```bash
cp config.example.yaml config.yaml
nano config.yaml
```

**PowerShell:**
```powershell
Copy-Item config.example.yaml config.yaml
notepad config.yaml
```

Main settings:

```yaml
azure:
  subscriptionId: "your-subscription-id"
  resourceGroup: "your-resource-group"
  clusterName: "your-aks-cluster"
  useManagedIdentity: true

healthChecks:
  interval: 30s
  unhealthyThreshold: 3
  checks:
    - node-condition
    - disk-pressure
    - memory-pressure

controller:
  reconcileInterval: 60s
  drainTimeout: 5m
  drainGracePeriod: 30s
  ignoreDaemonSets: true
  deleteLocalData: false
  maxConcurrentDrains: 1

autoscaling:
  enabled: true
  scaleUpThreshold: 0.8
  scaleDownThreshold: 0.3
  scaleUpCooldown: 5m
  scaleDownCooldown: 10m
  minNodes: 2
  maxNodes: 10
  nodePools:
    - agentpool

alerting:
  enabled: true
  slackWebhookUrl: "https://hooks.slack.com/services/xxx/yyy/zzz"
  slackChannel: "#k8s-alerts"
  webhookUrls: []
```

## Running Locally

**Prerequisites:**
- Go 1.21+
- kubectl configured with cluster access
- Azure CLI logged in (for Azure features)

**bash:**
```bash
go mod download
go run ./cmd/controller --config=./config.yaml
```

**PowerShell:**
```powershell
go mod download
go run ./cmd/controller --config=./config.yaml
```

Or use the Makefile (bash/Linux/Mac only):
```bash
make run
```

The controller will use your current kubeconfig context. To specify a different one:

**bash:**
```bash
go run ./cmd/controller --kubeconfig=/path/to/kubeconfig --config=./config.yaml
```

**PowerShell:**
```powershell
go run ./cmd/controller --kubeconfig=C:\path\to\kubeconfig --config=./config.yaml
```

## Building

**bash:**
```bash
# Build binary
make build

# Build Docker image
make docker-build

# Push to registry (edit IMAGE_NAME in Makefile first)
make docker-push
```

**PowerShell:**
```powershell
# Build binary
go build -o bin/node-lifecycle-controller.exe ./cmd/controller

# Build Docker image
docker build -t node-lifecycle-controller:latest .

# Tag and push (replace with your registry)
docker tag node-lifecycle-controller:latest yourregistry.azurecr.io/node-lifecycle-controller:latest
docker push yourregistry.azurecr.io/node-lifecycle-controller:latest
```

## Deploying to Kubernetes

1. Edit `deploy/configmap.yaml` with your configuration
2. Build and push the Docker image to your registry
3. Update the image in `deploy/deployment.yaml`
4. Deploy:

**bash:**
```bash
make deploy
```

**PowerShell:**
```powershell
kubectl apply -f deploy/rbac.yaml
kubectl apply -f deploy/configmap.yaml
kubectl apply -f deploy/deployment.yaml
```

This creates:
- Namespace `node-lifecycle-system`
- ServiceAccount with necessary RBAC permissions
- ConfigMap with controller configuration
- Deployment running the controller
- Service exposing metrics

### Azure Authentication in AKS

For the Azure features (autoscaling, metrics), the controller needs Azure credentials. Options:

1. **Managed Identity (recommended)**: Enable managed identity on your AKS cluster and assign it permissions to the cluster resource
2. **Service Principal**: Set `useManagedIdentity: false` and provide tenant/client/secret
3. **Default Credential Chain**: Works with Azure CLI login, environment variables, etc.

Required Azure RBAC permissions:
- `Microsoft.ContainerService/managedClusters/read`
- `Microsoft.ContainerService/managedClusters/agentPools/read`
- `Microsoft.ContainerService/managedClusters/agentPools/write`
- `Microsoft.Insights/metrics/read`

## Metrics

The controller exposes Prometheus metrics on `:8080/metrics`:

| Metric | Type | Description |
|--------|------|-------------|
| `node_lifecycle_health_status` | Gauge | 1 if node healthy, 0 if not |
| `node_lifecycle_node_count` | Gauge | Total nodes in cluster |
| `node_lifecycle_drain_total` | Counter | Drain operations by result |
| `node_lifecycle_cordon_total` | Counter | Cordon operations by result |
| `node_lifecycle_scale_total` | Counter | Scale operations by direction and result |
| `node_lifecycle_cluster_cpu_utilization` | Gauge | Cluster CPU from Azure Monitor |
| `node_lifecycle_cluster_memory_utilization` | Gauge | Cluster memory from Azure Monitor |
| `node_lifecycle_reconcile_duration_seconds` | Histogram | Reconcile loop duration |

## Grafana Dashboard

Import `dashboards/grafana.json` into Grafana. Requires a Prometheus datasource scraping the controller's metrics endpoint.

The dashboard shows:
- Healthy vs total node count
- Cluster CPU and memory utilization
- Node health over time
- Drain and scale operation counts
- Reconcile loop latency

## Alerting

Alerts are sent when:
- A node is marked unhealthy and remediation starts
- Drain succeeds or fails
- Scale up/down operations occur

Slack alerts include:
- Severity color coding (green/yellow/red)
- Node name and reason
- Timestamp

## Adding Custom Health Checks

Implement the `Check` interface in `pkg/health/checker.go`:

```go
type Check interface {
    Name() string
    Run(ctx context.Context, node *corev1.Node) (bool, string)
}
```

Return `(true, "")` for healthy, `(false, "reason")` for unhealthy.

Register your check in the `NewChecker` switch statement.

## Differences from Cluster Autoscaler

The standard Kubernetes Cluster Autoscaler scales based on pending pods. This controller adds:

- Custom health checks beyond what kubelet reports
- Proactive remediation of unhealthy nodes
- Azure Monitor integration for utilization-based decisions
- Slack/webhook alerting
- More control over drain behavior

They can run together if you disable the autoscaling feature here.
