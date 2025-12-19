package metrics

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/klog/v2"
)

type Collector struct {
	nodeHealth       *prometheus.GaugeVec
	nodeCount        prometheus.Gauge
	drainTotal       *prometheus.CounterVec
	cordonTotal      *prometheus.CounterVec
	scaleTotal       *prometheus.CounterVec
	clusterCPU       prometheus.Gauge
	clusterMemory    prometheus.Gauge
	reconcileDuration prometheus.Histogram

	mu sync.RWMutex
}

func NewCollector() *Collector {
	c := &Collector{
		nodeHealth: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "node_lifecycle_health_status",
				Help: "Health status of nodes (1 = healthy, 0 = unhealthy)",
			},
			[]string{"node"},
		),
		nodeCount: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "node_lifecycle_node_count",
				Help: "Total number of nodes in the cluster",
			},
		),
		drainTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "node_lifecycle_drain_total",
				Help: "Total number of node drain operations",
			},
			[]string{"node", "result"},
		),
		cordonTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "node_lifecycle_cordon_total",
				Help: "Total number of node cordon operations",
			},
			[]string{"node", "result"},
		),
		scaleTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "node_lifecycle_scale_total",
				Help: "Total number of scaling operations",
			},
			[]string{"nodepool", "direction", "result"},
		),
		clusterCPU: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "node_lifecycle_cluster_cpu_utilization",
				Help: "Cluster CPU utilization percentage",
			},
		),
		clusterMemory: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "node_lifecycle_cluster_memory_utilization",
				Help: "Cluster memory utilization percentage",
			},
		),
		reconcileDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "node_lifecycle_reconcile_duration_seconds",
				Help:    "Duration of reconciliation loops",
				Buckets: prometheus.DefBuckets,
			},
		),
	}

	prometheus.MustRegister(
		c.nodeHealth,
		c.nodeCount,
		c.drainTotal,
		c.cordonTotal,
		c.scaleTotal,
		c.clusterCPU,
		c.clusterMemory,
		c.reconcileDuration,
	)

	return c
}

func (c *Collector) SetNodeHealth(node string, healthy bool) {
	val := 0.0
	if healthy {
		val = 1.0
	}
	c.nodeHealth.WithLabelValues(node).Set(val)
}

func (c *Collector) IncNodeCount() {
	c.nodeCount.Inc()
}

func (c *Collector) DecNodeCount() {
	c.nodeCount.Dec()
}

func (c *Collector) IncDrainSuccess(node string) {
	c.drainTotal.WithLabelValues(node, "success").Inc()
}

func (c *Collector) IncDrainFailure(node string) {
	c.drainTotal.WithLabelValues(node, "failure").Inc()
}

func (c *Collector) IncCordonSuccess(node string) {
	c.cordonTotal.WithLabelValues(node, "success").Inc()
}

func (c *Collector) IncCordonFailure(node string) {
	c.cordonTotal.WithLabelValues(node, "failure").Inc()
}

func (c *Collector) IncScaleSuccess(nodePool, direction string) {
	c.scaleTotal.WithLabelValues(nodePool, direction, "success").Inc()
}

func (c *Collector) IncScaleFailure(nodePool, direction string) {
	c.scaleTotal.WithLabelValues(nodePool, direction, "failure").Inc()
}

func (c *Collector) SetClusterUtilization(cpu, memory float64) {
	c.clusterCPU.Set(cpu)
	c.clusterMemory.Set(memory)
}

func (c *Collector) ObserveReconcileDuration(seconds float64) {
	c.reconcileDuration.Observe(seconds)
}

func StartServer(addr string, collector *Collector) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	klog.Infof("starting metrics server on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		klog.Fatalf("metrics server error: %v", err)
	}
}
