package health

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/node-lifecycle-manager/pkg/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type CheckResult struct {
	Healthy bool
	Reasons []string
}

type Check interface {
	Name() string
	Run(ctx context.Context, node *corev1.Node) (bool, string)
}

type Checker struct {
	clientset kubernetes.Interface
	config    config.HealthCheckConfig
	checks    []Check
}

func NewChecker(clientset kubernetes.Interface, cfg config.HealthCheckConfig) *Checker {
	c := &Checker{
		clientset: clientset,
		config:    cfg,
		checks:    make([]Check, 0),
	}

	for _, name := range cfg.Checks {
		switch name {
		case "node-condition":
			c.checks = append(c.checks, &NodeConditionCheck{})
		case "kubelet":
			c.checks = append(c.checks, &KubeletCheck{clientset: clientset})
		case "disk-pressure":
			c.checks = append(c.checks, &DiskPressureCheck{})
		case "memory-pressure":
			c.checks = append(c.checks, &MemoryPressureCheck{})
		case "pid-pressure":
			c.checks = append(c.checks, &PIDPressureCheck{})
		case "network":
			c.checks = append(c.checks, &NetworkCheck{})
		default:
			klog.Warningf("unknown health check: %s", name)
		}
	}

	if len(c.checks) == 0 {
		c.checks = append(c.checks, &NodeConditionCheck{})
	}

	return c
}

func (c *Checker) Check(ctx context.Context, node *corev1.Node) CheckResult {
	result := CheckResult{Healthy: true}

	for _, check := range c.checks {
		healthy, reason := check.Run(ctx, node)
		if !healthy {
			result.Healthy = false
			result.Reasons = append(result.Reasons, fmt.Sprintf("%s: %s", check.Name(), reason))
		}
	}

	return result
}

type NodeConditionCheck struct{}

func (c *NodeConditionCheck) Name() string {
	return "node-condition"
}

func (c *NodeConditionCheck) Run(ctx context.Context, node *corev1.Node) (bool, string) {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			if condition.Status != corev1.ConditionTrue {
				return false, fmt.Sprintf("node not ready: %s", condition.Message)
			}
			
			if time.Since(condition.LastHeartbeatTime.Time) > 5*time.Minute {
				return false, "node heartbeat stale"
			}
		}
	}
	return true, ""
}

type KubeletCheck struct {
	clientset kubernetes.Interface
}

func (c *KubeletCheck) Name() string {
	return "kubelet"
}

func (c *KubeletCheck) Run(ctx context.Context, node *corev1.Node) (bool, string) {
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			url := fmt.Sprintf("https://%s:10250/healthz", addr.Address)
			
			client := &http.Client{Timeout: 5 * time.Second}
			req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
			
			resp, err := client.Do(req)
			if err != nil {
				return false, fmt.Sprintf("kubelet unreachable: %v", err)
			}
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				return false, fmt.Sprintf("kubelet unhealthy: status %d", resp.StatusCode)
			}
			return true, ""
		}
	}
	return true, ""
}

type DiskPressureCheck struct{}

func (c *DiskPressureCheck) Name() string {
	return "disk-pressure"
}

func (c *DiskPressureCheck) Run(ctx context.Context, node *corev1.Node) (bool, string) {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeDiskPressure {
			if condition.Status == corev1.ConditionTrue {
				return false, condition.Message
			}
		}
	}
	return true, ""
}

type MemoryPressureCheck struct{}

func (c *MemoryPressureCheck) Name() string {
	return "memory-pressure"
}

func (c *MemoryPressureCheck) Run(ctx context.Context, node *corev1.Node) (bool, string) {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeMemoryPressure {
			if condition.Status == corev1.ConditionTrue {
				return false, condition.Message
			}
		}
	}
	return true, ""
}

type PIDPressureCheck struct{}

func (c *PIDPressureCheck) Name() string {
	return "pid-pressure"
}

func (c *PIDPressureCheck) Run(ctx context.Context, node *corev1.Node) (bool, string) {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodePIDPressure {
			if condition.Status == corev1.ConditionTrue {
				return false, condition.Message
			}
		}
	}
	return true, ""
}

type NetworkCheck struct{}

func (c *NetworkCheck) Name() string {
	return "network"
}

func (c *NetworkCheck) Run(ctx context.Context, node *corev1.Node) (bool, string) {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeNetworkUnavailable {
			if condition.Status == corev1.ConditionTrue {
				return false, condition.Message
			}
		}
	}
	return true, ""
}
