package drain

import (
	"context"
	"fmt"
	"time"

	"github.com/node-lifecycle-manager/pkg/config"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type Drainer struct {
	clientset        kubernetes.Interface
	timeout          time.Duration
	gracePeriod      time.Duration
	ignoreDaemonSets bool
	deleteLocalData  bool
}

func NewDrainer(clientset kubernetes.Interface, cfg config.ControllerConfig) *Drainer {
	return &Drainer{
		clientset:        clientset,
		timeout:          cfg.DrainTimeout,
		gracePeriod:      cfg.DrainGracePeriod,
		ignoreDaemonSets: cfg.IgnoreDaemonSets,
		deleteLocalData:  cfg.DeleteLocalData,
	}
}

func (d *Drainer) Cordon(ctx context.Context, nodeName string) error {
	node, err := d.clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get node: %w", err)
	}

	if node.Spec.Unschedulable {
		return nil
	}

	node.Spec.Unschedulable = true
	_, err = d.clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update node: %w", err)
	}

	return nil
}

func (d *Drainer) Uncordon(ctx context.Context, nodeName string) error {
	node, err := d.clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get node: %w", err)
	}

	if !node.Spec.Unschedulable {
		return nil
	}

	node.Spec.Unschedulable = false
	_, err = d.clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update node: %w", err)
	}

	return nil
}

func (d *Drainer) Drain(ctx context.Context, nodeName string) error {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	pods, err := d.getPodsForDrain(ctx, nodeName)
	if err != nil {
		return fmt.Errorf("get pods: %w", err)
	}

	klog.Infof("draining %d pods from node %s", len(pods), nodeName)

	for _, pod := range pods {
		if err := d.evictPod(ctx, &pod); err != nil {
			return fmt.Errorf("evict pod %s/%s: %w", pod.Namespace, pod.Name, err)
		}
	}

	return d.waitForPodsToTerminate(ctx, nodeName)
}

func (d *Drainer) getPodsForDrain(ctx context.Context, nodeName string) ([]corev1.Pod, error) {
	fieldSelector := fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName})
	pods, err := d.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: fieldSelector.String(),
	})
	if err != nil {
		return nil, err
	}

	var result []corev1.Pod
	for _, pod := range pods.Items {
		if d.shouldEvictPod(&pod) {
			result = append(result, pod)
		}
	}
	return result, nil
}

func (d *Drainer) shouldEvictPod(pod *corev1.Pod) bool {
	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return false
	}

	if d.ignoreDaemonSets && d.isDaemonSetPod(pod) {
		return false
	}

	if d.isMirrorPod(pod) {
		return false
	}

	if !d.deleteLocalData && d.hasLocalStorage(pod) {
		klog.Warningf("pod %s/%s has local storage, skipping", pod.Namespace, pod.Name)
		return false
	}

	return true
}

func (d *Drainer) isDaemonSetPod(pod *corev1.Pod) bool {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func (d *Drainer) isMirrorPod(pod *corev1.Pod) bool {
	_, ok := pod.Annotations[corev1.MirrorPodAnnotationKey]
	return ok
}

func (d *Drainer) hasLocalStorage(pod *corev1.Pod) bool {
	for _, vol := range pod.Spec.Volumes {
		if vol.EmptyDir != nil {
			return true
		}
	}
	return false
}

func (d *Drainer) evictPod(ctx context.Context, pod *corev1.Pod) error {
	gracePeriod := int64(d.gracePeriod.Seconds())
	
	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		DeleteOptions: &metav1.DeleteOptions{
			GracePeriodSeconds: &gracePeriod,
		},
	}

	err := d.clientset.PolicyV1().Evictions(pod.Namespace).Evict(ctx, eviction)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		if apierrors.IsTooManyRequests(err) {
			klog.Warningf("eviction blocked by PDB for pod %s/%s, retrying", pod.Namespace, pod.Name)
			return d.retryEviction(ctx, pod)
		}
		return err
	}

	klog.V(2).Infof("evicted pod %s/%s", pod.Namespace, pod.Name)
	return nil
}

func (d *Drainer) retryEviction(ctx context.Context, pod *corev1.Pod) error {
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		err := d.evictPod(ctx, pod)
		if err == nil {
			return true, nil
		}
		if apierrors.IsTooManyRequests(err) {
			return false, nil
		}
		return false, err
	})
}

func (d *Drainer) waitForPodsToTerminate(ctx context.Context, nodeName string) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, d.timeout, true, func(ctx context.Context) (bool, error) {
		pods, err := d.getPodsForDrain(ctx, nodeName)
		if err != nil {
			return false, err
		}
		if len(pods) == 0 {
			return true, nil
		}
		klog.V(2).Infof("waiting for %d pods to terminate on node %s", len(pods), nodeName)
		return false, nil
	})
}
