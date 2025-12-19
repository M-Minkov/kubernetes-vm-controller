package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/node-lifecycle-manager/pkg/alerting"
	"github.com/node-lifecycle-manager/pkg/azure"
	"github.com/node-lifecycle-manager/pkg/config"
	"github.com/node-lifecycle-manager/pkg/drain"
	"github.com/node-lifecycle-manager/pkg/health"
	"github.com/node-lifecycle-manager/pkg/metrics"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

type NodeState struct {
	Name            string
	Healthy         bool
	LastCheck       time.Time
	UnhealthyCount  int
	Reasons         []string
	Cordoned        bool
	DrainInProgress bool
}

type Controller struct {
	clientset     kubernetes.Interface
	azureClient   *azure.Client
	healthChecker *health.Checker
	alerter       *alerting.Alerter
	metrics       *metrics.Collector
	config        *config.Config
	drainer       *drain.Drainer

	nodeLister   listersv1.NodeLister
	nodeInformer cache.SharedIndexInformer

	nodeStates map[string]*NodeState
	stateMu    sync.RWMutex

	lastScaleUp   time.Time
	lastScaleDown time.Time
	scaleMu       sync.Mutex
}

type Options struct {
	Clientset     kubernetes.Interface
	AzureClient   *azure.Client
	HealthChecker *health.Checker
	Alerter       *alerting.Alerter
	Metrics       *metrics.Collector
	Config        *config.Config
}

func New(opts Options) *Controller {
	return &Controller{
		clientset:     opts.Clientset,
		azureClient:   opts.AzureClient,
		healthChecker: opts.HealthChecker,
		alerter:       opts.Alerter,
		metrics:       opts.Metrics,
		config:        opts.Config,
		drainer:       drain.NewDrainer(opts.Clientset, opts.Config.Controller),
		nodeStates:    make(map[string]*NodeState),
	}
}

func (c *Controller) Run(ctx context.Context) error {
	factory := informers.NewSharedInformerFactory(c.clientset, time.Minute*5)
	c.nodeInformer = factory.Core().V1().Nodes().Informer()
	c.nodeLister = factory.Core().V1().Nodes().Lister()

	c.nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.onNodeAdd,
		UpdateFunc: c.onNodeUpdate,
		DeleteFunc: c.onNodeDelete,
	})

	factory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), c.nodeInformer.HasSynced) {
		return fmt.Errorf("failed to sync node cache")
	}

	klog.Info("node cache synced, starting reconciliation loop")

	ticker := time.NewTicker(c.config.Controller.ReconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			c.reconcile(ctx)
		}
	}
}

func (c *Controller) reconcile(ctx context.Context) {
	nodes, err := c.nodeLister.List(labels.Everything())
	if err != nil {
		klog.Errorf("failed to list nodes: %v", err)
		return
	}

	for _, node := range nodes {
		c.reconcileNode(ctx, node)
	}

	if c.config.Autoscaling.Enabled {
		c.evaluateAutoscaling(ctx, nodes)
	}
}

func (c *Controller) reconcileNode(ctx context.Context, node *corev1.Node) {
	result := c.healthChecker.Check(ctx, node)

	c.stateMu.Lock()
	state, exists := c.nodeStates[node.Name]
	if !exists {
		state = &NodeState{Name: node.Name}
		c.nodeStates[node.Name] = state
	}

	state.LastCheck = time.Now()
	state.Healthy = result.Healthy
	state.Reasons = result.Reasons
	state.Cordoned = node.Spec.Unschedulable

	if result.Healthy {
		state.UnhealthyCount = 0
	} else {
		state.UnhealthyCount++
	}

	unhealthyCount := state.UnhealthyCount
	drainInProgress := state.DrainInProgress
	c.stateMu.Unlock()

	c.metrics.SetNodeHealth(node.Name, result.Healthy)

	if !result.Healthy {
		klog.Warningf("node %s unhealthy: %v", node.Name, result.Reasons)

		if unhealthyCount >= c.config.HealthChecks.UnhealthyThreshold && !drainInProgress {
			c.handleUnhealthyNode(ctx, node, result.Reasons)
		}
	}
}

func (c *Controller) handleUnhealthyNode(ctx context.Context, node *corev1.Node, reasons []string) {
	c.stateMu.Lock()
	state := c.nodeStates[node.Name]
	if state.DrainInProgress {
		c.stateMu.Unlock()
		return
	}
	state.DrainInProgress = true
	c.stateMu.Unlock()

	defer func() {
		c.stateMu.Lock()
		state.DrainInProgress = false
		c.stateMu.Unlock()
	}()

	c.alerter.Send(alerting.Alert{
		Severity: alerting.SeverityWarning,
		Title:    fmt.Sprintf("Node %s marked unhealthy", node.Name),
		Message:  fmt.Sprintf("Reasons: %v. Starting remediation.", reasons),
		Labels:   map[string]string{"node": node.Name},
	})

	klog.Infof("cordoning node %s", node.Name)
	if err := c.drainer.Cordon(ctx, node.Name); err != nil {
		klog.Errorf("failed to cordon node %s: %v", node.Name, err)
		c.metrics.IncCordonFailure(node.Name)
		return
	}
	c.metrics.IncCordonSuccess(node.Name)

	klog.Infof("draining node %s", node.Name)
	if err := c.drainer.Drain(ctx, node.Name); err != nil {
		klog.Errorf("failed to drain node %s: %v", node.Name, err)
		c.metrics.IncDrainFailure(node.Name)

		c.alerter.Send(alerting.Alert{
			Severity: alerting.SeverityCritical,
			Title:    fmt.Sprintf("Failed to drain node %s", node.Name),
			Message:  fmt.Sprintf("Manual intervention required. Error: %v", err),
			Labels:   map[string]string{"node": node.Name},
		})
		return
	}
	c.metrics.IncDrainSuccess(node.Name)

	c.alerter.Send(alerting.Alert{
		Severity: alerting.SeverityInfo,
		Title:    fmt.Sprintf("Node %s drained successfully", node.Name),
		Message:  "Node has been cordoned and drained. Workloads migrated.",
		Labels:   map[string]string{"node": node.Name},
	})

	if c.config.Autoscaling.Enabled {
		c.requestNodeReplacement(ctx, node)
	}
}

func (c *Controller) requestNodeReplacement(ctx context.Context, node *corev1.Node) {
	nodePool := getNodePoolFromNode(node)
	if nodePool == "" {
		klog.Warningf("could not determine node pool for node %s", node.Name)
		return
	}

	klog.Infof("requesting replacement for node %s in pool %s", node.Name, nodePool)

	if err := c.azureClient.DeleteNode(ctx, nodePool, node.Name); err != nil {
		klog.Errorf("failed to delete node %s from azure: %v", node.Name, err)
	}
}

func (c *Controller) evaluateAutoscaling(ctx context.Context, nodes []*corev1.Node) {
	c.scaleMu.Lock()
	defer c.scaleMu.Unlock()

	klog.Infof("evaluating autoscaling with %d nodes", len(nodes))
	utilization := c.calculateClusterUtilization(ctx, nodes)
	klog.Infof("cluster utilization: CPU=%.2f%%, Memory=%.2f%%", utilization.CPU*100, utilization.Memory*100)
	c.metrics.SetClusterUtilization(utilization.CPU, utilization.Memory)

	if utilization.CPU > c.config.Autoscaling.ScaleUpThreshold ||
		utilization.Memory > c.config.Autoscaling.ScaleUpThreshold {
		klog.Infof("utilization above scale-up threshold (%.2f), considering scale up", c.config.Autoscaling.ScaleUpThreshold)
		c.considerScaleUp(ctx, nodes, utilization)
	}

	if utilization.CPU < c.config.Autoscaling.ScaleDownThreshold &&
		utilization.Memory < c.config.Autoscaling.ScaleDownThreshold {
		klog.Infof("utilization below scale-down threshold (%.2f), considering scale down", c.config.Autoscaling.ScaleDownThreshold)
		c.considerScaleDown(ctx, nodes, utilization)
	}
}

type ClusterUtilization struct {
	CPU    float64
	Memory float64
}

func (c *Controller) calculateClusterUtilization(ctx context.Context, nodes []*corev1.Node) ClusterUtilization {
	if c.azureClient == nil {
		return ClusterUtilization{}
	}

	cpuUtil, memUtil, err := c.azureClient.GetClusterUtilization(ctx)
	if err != nil {
		klog.Errorf("failed to get cluster utilization from azure: %v", err)
		return ClusterUtilization{}
	}

	return ClusterUtilization{
		CPU:    cpuUtil,
		Memory: memUtil,
	}
}

func (c *Controller) considerScaleUp(ctx context.Context, nodes []*corev1.Node, util ClusterUtilization) {
	if time.Since(c.lastScaleUp) < c.config.Autoscaling.ScaleUpCooldown {
		return
	}

	currentCount := len(nodes)
	if currentCount >= c.config.Autoscaling.MaxNodes {
		klog.Info("cluster at maximum node count, cannot scale up")
		return
	}

	nodePool := c.config.Autoscaling.NodePools[0]
	newCount := currentCount + 1

	klog.Infof("scaling up node pool %s from %d to %d nodes", nodePool, currentCount, newCount)

	if err := c.azureClient.ScaleNodePool(ctx, nodePool, newCount); err != nil {
		klog.Errorf("failed to scale up: %v", err)
		c.metrics.IncScaleFailure(nodePool, "up")
		return
	}

	c.lastScaleUp = time.Now()
	c.metrics.IncScaleSuccess(nodePool, "up")

	c.alerter.Send(alerting.Alert{
		Severity: alerting.SeverityInfo,
		Title:    "Cluster scaled up",
		Message:  fmt.Sprintf("Node pool %s scaled to %d nodes. CPU: %.1f%%, Memory: %.1f%%", nodePool, newCount, util.CPU*100, util.Memory*100),
		Labels:   map[string]string{"nodePool": nodePool, "action": "scale-up"},
	})
}

func (c *Controller) considerScaleDown(ctx context.Context, nodes []*corev1.Node, util ClusterUtilization) {
	klog.Infof("considerScaleDown: checking cooldown (lastScaleDown=%v, cooldown=%v)",
		c.lastScaleDown, c.config.Autoscaling.ScaleDownCooldown)

	if time.Since(c.lastScaleDown) < c.config.Autoscaling.ScaleDownCooldown {
		klog.Infof("scale-down cooldown active, skipping")
		return
	}

	currentCount := len(nodes)
	if currentCount <= c.config.Autoscaling.MinNodes {
		klog.Infof("at minimum nodes (%d/%d), cannot scale down", currentCount, c.config.Autoscaling.MinNodes)
		return
	}

	nodePool := c.config.Autoscaling.NodePools[0]
	newCount := currentCount - 1

	klog.Infof("scaling down node pool %s from %d to %d nodes", nodePool, currentCount, newCount)

	if err := c.azureClient.ScaleNodePool(ctx, nodePool, newCount); err != nil {
		klog.Errorf("failed to scale down: %v", err)
		c.metrics.IncScaleFailure(nodePool, "down")
		return
	}

	c.lastScaleDown = time.Now()
	c.metrics.IncScaleSuccess(nodePool, "down")

	c.alerter.Send(alerting.Alert{
		Severity: alerting.SeverityInfo,
		Title:    "Cluster scaled down",
		Message:  fmt.Sprintf("Node pool %s scaled to %d nodes. CPU: %.1f%%, Memory: %.1f%%", nodePool, newCount, util.CPU*100, util.Memory*100),
		Labels:   map[string]string{"nodePool": nodePool, "action": "scale-down"},
	})
}

func (c *Controller) onNodeAdd(obj interface{}) {
	node := obj.(*corev1.Node)
	klog.V(2).Infof("node added: %s", node.Name)
	c.metrics.IncNodeCount()
}

func (c *Controller) onNodeUpdate(oldObj, newObj interface{}) {
	node := newObj.(*corev1.Node)
	klog.V(4).Infof("node updated: %s", node.Name)
}

func (c *Controller) onNodeDelete(obj interface{}) {
	node := obj.(*corev1.Node)
	klog.V(2).Infof("node deleted: %s", node.Name)

	c.stateMu.Lock()
	delete(c.nodeStates, node.Name)
	c.stateMu.Unlock()

	c.metrics.DecNodeCount()
}

func getNodePoolFromNode(node *corev1.Node) string {
	if pool, ok := node.Labels["agentpool"]; ok {
		return pool
	}
	if pool, ok := node.Labels["kubernetes.azure.com/agentpool"]; ok {
		return pool
	}
	return ""
}
