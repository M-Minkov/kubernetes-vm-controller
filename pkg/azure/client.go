package azure

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/azquery"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v4"

	"github.com/node-lifecycle-manager/pkg/config"
	"k8s.io/klog/v2"
)

type Client struct {
	config          config.AzureConfig
	credential      azcore.TokenCredential
	aksClient       *armcontainerservice.ManagedClustersClient
	agentPoolClient *armcontainerservice.AgentPoolsClient
	metricsClient   *azquery.MetricsClient
}

func NewClient(cfg config.AzureConfig) (*Client, error) {
	if cfg.SubscriptionID == "" {
		klog.Warning("azure subscription not configured, azure features disabled")
		return nil, nil
	}

	var cred azcore.TokenCredential
	var err error

	if cfg.UseManagedIdentity {
		cred, err = azidentity.NewManagedIdentityCredential(nil)
	} else if cfg.ClientSecret != "" {
		cred, err = azidentity.NewClientSecretCredential(cfg.TenantID, cfg.ClientID, cfg.ClientSecret, nil)
	} else {
		cred, err = azidentity.NewDefaultAzureCredential(nil)
	}
	if err != nil {
		return nil, fmt.Errorf("create credential: %w", err)
	}

	aksClient, err := armcontainerservice.NewManagedClustersClient(cfg.SubscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("create aks client: %w", err)
	}

	agentPoolClient, err := armcontainerservice.NewAgentPoolsClient(cfg.SubscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("create agent pool client: %w", err)
	}

	metricsClient, err := azquery.NewMetricsClient(cred, nil)
	if err != nil {
		return nil, fmt.Errorf("create metrics client: %w", err)
	}

	return &Client{
		config:          cfg,
		credential:      cred,
		aksClient:       aksClient,
		agentPoolClient: agentPoolClient,
		metricsClient:   metricsClient,
	}, nil
}

func (c *Client) ScaleNodePool(ctx context.Context, nodePoolName string, count int) error {
	pool, err := c.agentPoolClient.Get(ctx, c.config.ResourceGroup, c.config.ClusterName, nodePoolName, nil)
	if err != nil {
		return fmt.Errorf("get agent pool: %w", err)
	}

	pool.Properties.Count = to.Ptr(int32(count))

	poller, err := c.agentPoolClient.BeginCreateOrUpdate(
		ctx,
		c.config.ResourceGroup,
		c.config.ClusterName,
		nodePoolName,
		pool.AgentPool,
		nil,
	)
	if err != nil {
		return fmt.Errorf("begin scale: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("scale operation: %w", err)
	}

	return nil
}

func (c *Client) DeleteNode(ctx context.Context, nodePoolName string, nodeName string) error {
	pool, err := c.agentPoolClient.Get(ctx, c.config.ResourceGroup, c.config.ClusterName, nodePoolName, nil)
	if err != nil {
		return fmt.Errorf("get agent pool: %w", err)
	}

	if pool.Properties.Count != nil && *pool.Properties.Count > 1 {
		newCount := *pool.Properties.Count - 1
		pool.Properties.Count = to.Ptr(newCount)

		poller, err := c.agentPoolClient.BeginCreateOrUpdate(
			ctx,
			c.config.ResourceGroup,
			c.config.ClusterName,
			nodePoolName,
			pool.AgentPool,
			nil,
		)
		if err != nil {
			return fmt.Errorf("begin delete: %w", err)
		}

		_, err = poller.PollUntilDone(ctx, nil)
		if err != nil {
			return fmt.Errorf("delete operation: %w", err)
		}
	}

	return nil
}

func (c *Client) GetClusterUtilization(ctx context.Context) (cpuPercent, memoryPercent float64, err error) {
	resourceID := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerService/managedClusters/%s",
		c.config.SubscriptionID,
		c.config.ResourceGroup,
		c.config.ClusterName,
	)

	endTime := time.Now()
	startTime := endTime.Add(-5 * time.Minute)
	timeInterval := azquery.NewTimeInterval(startTime, endTime)

	resp, err := c.metricsClient.QueryResource(ctx, resourceID, &azquery.MetricsClientQueryResourceOptions{
		Timespan:    to.Ptr(timeInterval),
		Interval:    to.Ptr("PT1M"),
		MetricNames: to.Ptr("node_cpu_usage_percentage,node_memory_working_set_percentage"),
		Aggregation: to.SliceOfPtrs(azquery.AggregationTypeAverage),
	})
	if err != nil {
		return 0, 0, fmt.Errorf("query metrics: %w", err)
	}

	for _, metric := range resp.Value {
		if metric.Name == nil || metric.Name.Value == nil {
			continue
		}

		var value float64
		for _, ts := range metric.TimeSeries {
			for _, dp := range ts.Data {
				if dp.Average != nil {
					value = *dp.Average
				}
			}
		}

		switch *metric.Name.Value {
		case "node_cpu_usage_percentage":
			cpuPercent = value / 100
		case "node_memory_working_set_percentage":
			memoryPercent = value / 100
		}
	}

	return cpuPercent, memoryPercent, nil
}

func (c *Client) GetNodePoolInfo(ctx context.Context, nodePoolName string) (*NodePoolInfo, error) {
	pool, err := c.agentPoolClient.Get(ctx, c.config.ResourceGroup, c.config.ClusterName, nodePoolName, nil)
	if err != nil {
		return nil, fmt.Errorf("get agent pool: %w", err)
	}

	info := &NodePoolInfo{
		Name:      nodePoolName,
		VMSize:    "",
		NodeCount: 0,
		MinCount:  0,
		MaxCount:  0,
	}

	if pool.Properties != nil {
		if pool.Properties.VMSize != nil {
			info.VMSize = *pool.Properties.VMSize
		}
		if pool.Properties.Count != nil {
			info.NodeCount = int(*pool.Properties.Count)
		}
		if pool.Properties.MinCount != nil {
			info.MinCount = int(*pool.Properties.MinCount)
		}
		if pool.Properties.MaxCount != nil {
			info.MaxCount = int(*pool.Properties.MaxCount)
		}
	}

	return info, nil
}

type NodePoolInfo struct {
	Name      string
	VMSize    string
	NodeCount int
	MinCount  int
	MaxCount  int
}
