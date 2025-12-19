package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/node-lifecycle-manager/pkg/alerting"
	"github.com/node-lifecycle-manager/pkg/azure"
	"github.com/node-lifecycle-manager/pkg/config"
	"github.com/node-lifecycle-manager/pkg/controller"
	"github.com/node-lifecycle-manager/pkg/health"
	"github.com/node-lifecycle-manager/pkg/metrics"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

func main() {
	var kubeconfig string
	var configPath string
	var metricsAddr string

	flag.StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig file, leave empty for in-cluster")
	flag.StringVar(&configPath, "config", "/etc/node-lifecycle/config.yaml", "path to configuration file")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "address for metrics server")
	flag.Parse()

	klog.InitFlags(nil)

	cfg, err := config.Load(configPath)
	if err != nil {
		klog.Fatalf("failed to load config: %v", err)
	}

	k8sConfig, err := getKubernetesConfig(kubeconfig)
	if err != nil {
		klog.Fatalf("failed to get kubernetes config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		klog.Fatalf("failed to create kubernetes client: %v", err)
	}

	azureClient, err := azure.NewClient(cfg.Azure)
	if err != nil {
		klog.Fatalf("failed to create azure client: %v", err)
	}

	alerter := alerting.NewAlerter(cfg.Alerting)
	metricsCollector := metrics.NewCollector()
	healthChecker := health.NewChecker(clientset, cfg.HealthChecks)

	go metrics.StartServer(metricsAddr, metricsCollector)

	ctrl := controller.New(controller.Options{
		Clientset:     clientset,
		AzureClient:   azureClient,
		HealthChecker: healthChecker,
		Alerter:       alerter,
		Metrics:       metricsCollector,
		Config:        cfg,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		klog.Info("received shutdown signal")
		cancel()
	}()

	klog.Info("starting node lifecycle controller")
	if err := ctrl.Run(ctx); err != nil {
		klog.Fatalf("controller error: %v", err)
	}
}

func getKubernetesConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}

	cfg, err := rest.InClusterConfig()
	if err != nil {
		home, _ := os.UserHomeDir()
		return clientcmd.BuildConfigFromFlags("", home+"/.kube/config")
	}
	return cfg, nil
}
