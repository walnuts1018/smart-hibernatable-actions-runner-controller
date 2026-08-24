package remotecluster

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

// Provider provides cached client and health operations for remote RunnerClusters.
type Provider interface {
	// GetClient returns a controller-runtime Client for the remote cluster.
	GetClient(ctx context.Context, cluster *ghav1alpha1.RunnerCluster) (client.Client, error)

	// CheckHealth performs a health and connectivity check on the remote cluster API.
	CheckHealth(ctx context.Context, cluster *ghav1alpha1.RunnerCluster) error

	// GetNode retrieves a Node by name from the remote cluster.
	GetNode(ctx context.Context, cluster *ghav1alpha1.RunnerCluster, nodeName string) (*corev1.Node, error)

	// GetClusterUID retrieves the unique cluster UID (kube-system namespace UID) from the remote cluster.
	GetClusterUID(ctx context.Context, cluster *ghav1alpha1.RunnerCluster) (string, error)

	// InvalidateCache drops any cached client for the given cluster and closes idle connections.
	InvalidateCache(key client.ObjectKey)
}

type cachedClient struct {
	client          client.Client
	discoveryClient discovery.DiscoveryInterface
	httpClient      *http.Client
	resourceVersion string
}

type providerImpl struct {
	localClient client.Client
	scheme      *runtime.Scheme
	cacheMu     sync.RWMutex
	clients     map[string]*cachedClient
}

// NewProvider creates a new Provider.
func NewProvider(localClient client.Client, scheme *runtime.Scheme) Provider {
	return &providerImpl{
		localClient: localClient,
		scheme:      scheme,
		clients:     make(map[string]*cachedClient),
	}
}

func (p *providerImpl) getKubeconfig(ctx context.Context, cluster *ghav1alpha1.RunnerCluster) ([]byte, string, error) {
	if cluster.Namespace == "" {
		return nil, "", fmt.Errorf("runner cluster namespace must not be empty")
	}
	secretRef := cluster.Spec.KubeconfigSecretRef
	namespace := cluster.Namespace

	var secret corev1.Secret
	if err := p.localClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretRef.Name}, &secret); err != nil {
		return nil, "", fmt.Errorf("failed to get kubeconfig secret %s/%s: %w", namespace, secretRef.Name, err)
	}

	key := secretRef.Key
	if key == "" {
		key = "kubeconfig"
	}

	data, ok := secret.Data[key]
	if !ok {
		return nil, "", fmt.Errorf("key %q not found in kubeconfig secret %s/%s", key, namespace, secretRef.Name)
	}

	return data, secret.ResourceVersion, nil
}

func (p *providerImpl) getCachedOrCreate(ctx context.Context, cluster *ghav1alpha1.RunnerCluster) (*cachedClient, error) {
	if cluster.Namespace == "" {
		return nil, fmt.Errorf("runner cluster namespace must not be empty")
	}
	if cluster.Name == "" {
		return nil, fmt.Errorf("runner cluster name must not be empty")
	}

	kubeconfigData, rv, err := p.getKubeconfig(ctx, cluster)
	if err != nil {
		return nil, err
	}

	clusterKey := fmt.Sprintf("%s/%s", cluster.Namespace, cluster.Name)

	p.cacheMu.RLock()
	cached, ok := p.clients[clusterKey]
	if ok && cached.resourceVersion == rv {
		p.cacheMu.RUnlock()
		return cached, nil
	}
	p.cacheMu.RUnlock()

	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()

	// Double check
	if cached, ok := p.clients[clusterKey]; ok && cached.resourceVersion == rv {
		return cached, nil
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubeconfig for cluster %s: %w", clusterKey, err)
	}

	httpClient, err := rest.HTTPClientFor(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create http client for remote cluster %s: %w", clusterKey, err)
	}

	cl, err := client.New(restConfig, client.Options{
		Scheme:     p.scheme,
		HTTPClient: httpClient,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create client for remote cluster %s: %w", clusterKey, err)
	}

	disc, err := discovery.NewDiscoveryClientForConfigAndClient(restConfig, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client for remote cluster %s: %w", clusterKey, err)
	}

	newEntry := &cachedClient{
		client:          cl,
		discoveryClient: disc,
		httpClient:      httpClient,
		resourceVersion: rv,
	}

	old := p.clients[clusterKey]
	p.clients[clusterKey] = newEntry
	if old != nil && old.httpClient != nil {
		old.httpClient.CloseIdleConnections()
	}

	return newEntry, nil
}

func (p *providerImpl) GetClient(ctx context.Context, cluster *ghav1alpha1.RunnerCluster) (client.Client, error) {
	entry, err := p.getCachedOrCreate(ctx, cluster)
	if err != nil {
		return nil, err
	}
	return entry.client, nil
}

func (p *providerImpl) CheckHealth(ctx context.Context, cluster *ghav1alpha1.RunnerCluster) error {
	entry, err := p.getCachedOrCreate(ctx, cluster)
	if err != nil {
		return err
	}

	timeout := 5 * time.Second
	if cluster.Spec.Readiness.APIRequestTimeout != nil && cluster.Spec.Readiness.APIRequestTimeout.Duration > 0 {
		timeout = cluster.Spec.Readiness.APIRequestTimeout.Duration
	}

	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if _, err := entry.discoveryClient.RESTClient().Get().AbsPath("/readyz").DoRaw(checkCtx); err != nil {
		return fmt.Errorf("remote API server is not ready: %w", err)
	}

	return nil
}

func (p *providerImpl) GetNode(ctx context.Context, cluster *ghav1alpha1.RunnerCluster, nodeName string) (*corev1.Node, error) {
	cl, err := p.GetClient(ctx, cluster)
	if err != nil {
		return nil, err
	}

	var node corev1.Node
	if err := cl.Get(ctx, client.ObjectKey{Name: nodeName}, &node); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get node %q from remote cluster: %w", nodeName, err)
	}

	return &node, nil
}

func (p *providerImpl) GetClusterUID(ctx context.Context, cluster *ghav1alpha1.RunnerCluster) (string, error) {
	cl, err := p.GetClient(ctx, cluster)
	if err != nil {
		return "", err
	}

	var ns corev1.Namespace
	if err := cl.Get(ctx, client.ObjectKey{Name: "kube-system"}, &ns); err != nil {
		return "", fmt.Errorf("failed to get kube-system namespace from remote cluster: %w", err)
	}

	return string(ns.UID), nil
}

func (p *providerImpl) InvalidateCache(key client.ObjectKey) {
	clusterKey := key.String()
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	if cached, ok := p.clients[clusterKey]; ok {
		if cached.httpClient != nil {
			cached.httpClient.CloseIdleConnections()
		}
		delete(p.clients, clusterKey)
	}
}

// IsNodeReady checks if a Kubernetes Node has ConditionReady=True.
func IsNodeReady(node *corev1.Node) bool {
	if node == nil {
		return false
	}
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}
