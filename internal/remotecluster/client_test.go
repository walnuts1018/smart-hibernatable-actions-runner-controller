package remotecluster

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

func makeKubeconfig(serverURL string) []byte {
	return fmt.Appendf(nil, `
apiVersion: v1
clusters:
- cluster:
    server: %s
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
kind: Config
preferences: {}
users:
- name: test-user
  user:
    token: test-token
`, serverURL)
}

func TestProvider_CheckHealth_ReadyzFailure(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	// Mock server that returns 500 on /readyz but 200 on /version
	readyzStatus := http.StatusInternalServerError
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			w.WriteHeader(readyzStatus)
			_, _ = w.Write([]byte("not ready"))
		case "/version":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"major":"1","minor":"28"}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	secret := &corev1.Secret{
		Namespace:       "test-ns",
		Name:            "remote-kubeconfig",
		ResourceVersion: "1",
		Data: map[string][]byte{
			"kubeconfig": makeKubeconfig(server.URL),
		},
	}

	localClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	provider := NewProvider(localClient, scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Namespace: "test-ns",
		Name:      "test-cluster",
		Spec: ghav1alpha1.RunnerClusterSpec{
			KubeconfigSecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "remote-kubeconfig",
				},
				Key: "kubeconfig",
			},
		},
	}

	// 1. readyz returns 500 => CheckHealth should FAIL (and NOT fallback to /version)
	err := provider.CheckHealth(context.Background(), cluster)
	if err == nil {
		t.Fatalf("expected CheckHealth to fail when /readyz returns 500, but got nil")
	}

	// 2. readyz returns 200 => CheckHealth should SUCCEED
	readyzStatus = http.StatusOK
	err = provider.CheckHealth(context.Background(), cluster)
	if err != nil {
		t.Fatalf("expected CheckHealth to succeed when /readyz returns 200, got: %v", err)
	}
}

func TestProvider_ConcurrentAccess(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"major":"1","minor":"28"}`))
	}))
	defer server.Close()

	secret := &corev1.Secret{
		Namespace:       "test-ns",
		Name:            "remote-kubeconfig",
		ResourceVersion: "1",
		Data: map[string][]byte{
			"kubeconfig": makeKubeconfig(server.URL),
		},
	}

	localClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	provider := NewProvider(localClient, scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Namespace: "test-ns",
		Name:      "test-cluster",
		Spec: ghav1alpha1.RunnerClusterSpec{
			KubeconfigSecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "remote-kubeconfig",
				},
			},
		},
	}

	// Concurrently call GetClient, CheckHealth, and InvalidateCache
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for j := range 10 {
				_, _ = provider.GetClient(context.Background(), cluster)
				_ = provider.CheckHealth(context.Background(), cluster)
				if j%3 == 0 {
					provider.InvalidateCache(client.ObjectKey{Namespace: "test-ns", Name: "test-cluster"})
				}
			}
		})
	}
	wg.Wait()
}

func TestProvider_CacheReplacedOnSecretUpdate(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server2.Close()

	secret := &corev1.Secret{
		Namespace:       "test-ns",
		Name:            "remote-kubeconfig",
		ResourceVersion: "1",
		Data: map[string][]byte{
			"kubeconfig": makeKubeconfig(server1.URL),
		},
	}

	localClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	provider := NewProvider(localClient, scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Namespace: "test-ns",
		Name:      "test-cluster",
		Spec: ghav1alpha1.RunnerClusterSpec{
			KubeconfigSecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "remote-kubeconfig",
				},
			},
		},
	}

	cl1, err := provider.GetClient(context.Background(), cluster)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Update secret in localClient with new ResourceVersion
	var curSecret corev1.Secret
	if err := localClient.Get(context.Background(), client.ObjectKey{Namespace: "test-ns", Name: "remote-kubeconfig"}, &curSecret); err != nil {
		t.Fatalf("failed to get current secret: %v", err)
	}
	curSecret.Data["kubeconfig"] = makeKubeconfig(server2.URL)
	if err := localClient.Update(context.Background(), &curSecret); err != nil {
		t.Fatalf("failed to update secret: %v", err)
	}

	cl2, err := provider.GetClient(context.Background(), cluster)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cl1 == cl2 {
		t.Errorf("expected new client instance after secret ResourceVersion update, but got same instance")
	}
}

func TestProvider_EmptyNamespaceValidation(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	localClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	provider := NewProvider(localClient, scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Namespace: "",
		Name:      "test-cluster",
	}

	_, err := provider.GetClient(context.Background(), cluster)
	if err == nil {
		t.Fatalf("expected error for empty namespace, got nil")
	}
}

func TestProvider_InsecureSkipTLSVerify(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	// TLS server with self-signed certificate
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer tlsServer.Close()

	secret := &corev1.Secret{
		Namespace:       "test-ns",
		Name:            "remote-kubeconfig",
		ResourceVersion: "1",
		Data: map[string][]byte{
			// Server URL is https://... without CA certificate
			"kubeconfig": makeKubeconfig(tlsServer.URL),
		},
	}

	localClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	provider := NewProvider(localClient, scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Namespace: "test-ns",
		Name:      "test-cluster",
		Spec: ghav1alpha1.RunnerClusterSpec{
			KubeconfigSecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "remote-kubeconfig",
				},
			},
		},
	}

	// 1. Without InsecureSkipTLSVerify, CheckHealth should fail due to self-signed cert
	err := provider.CheckHealth(context.Background(), cluster)
	if err == nil {
		t.Fatalf("expected CheckHealth to fail with self-signed cert when InsecureSkipTLSVerify is not enabled")
	}

	// 2. Enable InsecureSkipTLSVerify: true
	insecureTrue := true
	cluster.Spec.Connection = &ghav1alpha1.RunnerClusterConnectionSpec{
		InsecureSkipTLSVerify: &insecureTrue,
	}

	err = provider.CheckHealth(context.Background(), cluster)
	if err != nil {
		t.Fatalf("expected CheckHealth to succeed when InsecureSkipTLSVerify is true, got: %v", err)
	}
}

func TestIsNodeReady(t *testing.T) {
	// Nil node
	if IsNodeReady(nil) {
		t.Errorf("expected false for nil node")
	}

	// Node without conditions
	nodeWithoutConds := &corev1.Node{}
	if IsNodeReady(nodeWithoutConds) {
		t.Errorf("expected false for node without conditions")
	}

	// Node with Ready=False
	nodeNotReady := &corev1.Node{
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
			},
		},
	}
	if IsNodeReady(nodeNotReady) {
		t.Errorf("expected false for node with Ready=False")
	}

	// Node with Ready=True
	nodeReady := &corev1.Node{
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
	if !IsNodeReady(nodeReady) {
		t.Errorf("expected true for node with Ready=True")
	}
}

func TestProvider_InvalidateCache(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	localClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	provider := NewProvider(localClient, scheme)

	// Invalidate non-cached key should not panic
	provider.InvalidateCache(client.ObjectKey{Namespace: "default", Name: "cluster-1"})
}

func TestProvider_GetNodeAndClusterUID(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"versions":["v1"]}`))
		case "/api/v1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"resources": [
					{"name": "nodes", "namespaced": false, "kind": "Node"},
					{"name": "namespaces", "namespaced": false, "kind": "Namespace"}
				]
			}`))
		case "/api/v1/nodes/node-1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Node","metadata":{"name":"node-1"}}`))
		case "/api/v1/nodes/missing-node":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"NotFound","code":404}`))
		case "/api/v1/namespaces/kube-system":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"kube-system","uid":"uid-kube-system-12345"}}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"major":"1","minor":"28"}`))
		}
	}))
	defer server.Close()

	secret := &corev1.Secret{
		Namespace:       "test-ns",
		Name:            "remote-kubeconfig",
		ResourceVersion: "1",
		Data: map[string][]byte{
			"kubeconfig": makeKubeconfig(server.URL),
		},
	}

	localClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	provider := NewProvider(localClient, scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Namespace: "test-ns",
		Name:      "test-cluster",
		Spec: ghav1alpha1.RunnerClusterSpec{
			KubeconfigSecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "remote-kubeconfig",
				},
			},
		},
	}

	// 1. GetNode for existing node
	node, err := provider.GetNode(t.Context(), cluster, "node-1")
	if err != nil {
		t.Fatalf("unexpected error getting node: %v", err)
	}
	if node == nil || node.Name != "node-1" {
		t.Errorf("expected node-1, got %v", node)
	}

	// 2. GetNode for missing node (returns nil, nil)
	missingNode, err := provider.GetNode(t.Context(), cluster, "missing-node")
	if err != nil {
		t.Fatalf("unexpected error getting missing node: %v", err)
	}
	if missingNode != nil {
		t.Errorf("expected nil for missing node, got %v", missingNode)
	}

	// 3. GetClusterUID
	uid, err := provider.GetClusterUID(t.Context(), cluster)
	if err != nil {
		t.Fatalf("unexpected error getting cluster UID: %v", err)
	}
	if uid != "uid-kube-system-12345" {
		t.Errorf("expected UID 'uid-kube-system-12345', got %q", uid)
	}
}
