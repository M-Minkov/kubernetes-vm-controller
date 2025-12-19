# Run locally (quick start)

This runs the controller on your machine using your current kubeconfig context.

Most examples below use PowerShell syntax. If you're on macOS/Linux, the steps are the same—use your shell equivalents.

## Prereqs

- Go (same major version as `go.mod`)
- kubectl
- Azure CLI (only required if you enable Azure features)

Verify:

```powershell
go version
kubectl version --client
az version
```

## 1) Point kubectl at your cluster

If you already have kubeconfig set up, skip to the next section.

```powershell
az login
az account set --subscription "<SUBSCRIPTION_ID_OR_NAME>"

az aks get-credentials --resource-group "<AKS_RG>" --name "<AKS_NAME>" --overwrite-existing
kubectl config current-context
kubectl get nodes
```

If you get `Forbidden` errors later and just want to test quickly:

```powershell
az aks get-credentials --admin --resource-group "<AKS_RG>" --name "<AKS_NAME>" --overwrite-existing
```

## 2) Create a local config

From the repo root:

```powershell
Set-Location "C:\path\to\kubernetes-vm-controller"

Copy-Item .\config.example.yaml .\config.yaml -Force
notepad .\config.yaml
```

Minimum config to start without Azure:

- Set `autoscaling.enabled: false`
- Leave `azure.subscriptionId` empty (or remove the `azure:` block)

Notes:

- If `azure.subscriptionId` is empty, Azure features are disabled.
- Draining nodes requires permissions to cordon/drain/evict; use an identity with the right RBAC.

## 3) Run

```powershell
Set-Location "C:\path\to\kubernetes-vm-controller"

go mod download

go run .\cmd\controller --config .\config.yaml
```

Optional flags:

```powershell
# Use a specific kubeconfig
go run .\cmd\controller --kubeconfig "C:\path\to\kubeconfig" --config .\config.yaml

# Change metrics bind address (default is :8080)
go run .\cmd\controller --config .\config.yaml --metrics-addr ":8081"
```

## 4) Verify it is working

In another terminal:

```powershell
kubectl get nodes
```

If the process is running, it also starts a metrics server (default `http://localhost:8080/metrics`).

## Stop

Press `Ctrl+C` in the terminal running the controller.

## Troubleshooting

### "failed to get kubernetes config" / it tries in-cluster config

Make sure you have a kubeconfig and a current context:

```powershell
kubectl config current-context
kubectl get nodes
```

If needed, set `--kubeconfig` explicitly:

```powershell
go run .\cmd\controller --kubeconfig "$HOME\.kube\config" --config .\config.yaml
```

### Forbidden / RBAC errors

Your kubeconfig user doesn’t have the permissions the controller needs.

Quick checks:

```powershell
kubectl auth can-i list nodes
kubectl auth can-i patch nodes
kubectl auth can-i create pods -n kube-system
```

### Azure auth errors

If you are not using Azure features, keep `azure.subscriptionId` empty.

If you do want Azure features, login first:

```powershell
az login
az account set --subscription "<SUBSCRIPTION_ID_OR_NAME>"
```
