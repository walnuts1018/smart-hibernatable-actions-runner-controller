package listener

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/actions/scaleset"
	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

func TestScalerHandler_HandleDesiredRunnerCount(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	nodePool := &ghav1alpha1.RunnerNodePool{}
	nodePool.Name = "pool-1"
	nodePool.Namespace = "default"
	nodePool.Spec.MachineSelector.MatchLabels = map[string]string{"pool": "pool-1"}

	machine := &ghav1alpha1.RunnerMachine{}
	machine.Name = "m1"
	machine.Namespace = "default"
	machine.Labels = map[string]string{"pool": "pool-1"}
	machine.Spec.Capacity.Runners = 4

	scaleSet := &ghav1alpha1.RunnerScaleSet{}
	scaleSet.Name = "test-ss"
	scaleSet.Namespace = "default"
	scaleSet.Spec.NodePoolRef.Name = "pool-1"
	scaleSet.Spec.Scaling.MinRunners = 0
	scaleSet.Spec.Scaling.MaxRunners = 10
	scaleSet.Status.EffectiveMaxRunners = 4

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nodePool, machine, scaleSet).WithStatusSubresource(scaleSet).Build()
	tracker := NewReadinessTracker()

	scaler := NewScalerHandler(fakeClient, "default", "test-ss", tracker)

	count, err := scaler.HandleDesiredRunnerCount(context.Background(), 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}

	var updated ghav1alpha1.RunnerScaleSet
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-ss"}, &updated); err != nil {
		t.Fatalf("failed to get updated scaleSet: %v", err)
	}

	if updated.Status.DesiredRunners != 2 {
		t.Fatalf("expected DesiredRunners 2, got %d", updated.Status.DesiredRunners)
	}
	if updated.Status.GitHub.AssignedJobs != 2 {
		t.Fatalf("expected AssignedJobs 2, got %d", updated.Status.GitHub.AssignedJobs)
	}
	if !updated.Status.Listener.Ready {
		t.Fatal("expected Listener.Ready to be true")
	}
	if !tracker.initialStatisticsReceived {
		t.Fatal("expected initialStatisticsReceived to be true")
	}

	// declared capacity (4) を超える要求 (例えば6) が来た場合、4にcapされることを検証
	_, err = scaler.HandleDesiredRunnerCount(context.Background(), 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-ss"}, &updated); err != nil {
		t.Fatalf("failed to get updated scaleSet: %v", err)
	}
	if updated.Status.DesiredRunners != 4 {
		t.Fatalf("expected DesiredRunners to be capped at 4, got %d", updated.Status.DesiredRunners)
	}
}

func TestScalerHandler_HandleJobStartedAndCompleted(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	epRunner := &ghav1alpha1.EphemeralRunner{}
	epRunner.Name = "runner-1"
	epRunner.Namespace = "default"
	epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhasePending

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(epRunner).WithStatusSubresource(epRunner).Build()
	scaler := NewScalerHandler(fakeClient, "default", "test-ss", nil)

	jobStarted := &scaleset.JobStarted{}
	jobStarted.RunnerName = "runner-1"
	jobStarted.RunnerID = 12345
	jobStarted.JobID = "67890"

	err := scaler.HandleJobStarted(context.Background(), jobStarted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updated ghav1alpha1.EphemeralRunner
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "runner-1"}, &updated); err != nil {
		t.Fatalf("failed to get runner: %v", err)
	}
	if updated.Status.Phase != ghav1alpha1.EphemeralRunnerPhaseBusy {
		t.Fatalf("expected phase Busy, got %s", updated.Status.Phase)
	}
	if updated.Status.GitHub.RunnerID != 12345 {
		t.Fatalf("expected runnerID 12345, got %d", updated.Status.GitHub.RunnerID)
	}
	if updated.Status.GitHub.JobID != 67890 {
		t.Fatalf("expected jobID 67890, got %d", updated.Status.GitHub.JobID)
	}

	jobCompleted := &scaleset.JobCompleted{}
	jobCompleted.RunnerName = "runner-1"

	err = scaler.HandleJobCompleted(context.Background(), jobCompleted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "runner-1"}, &updated); err != nil {
		t.Fatalf("failed to get runner: %v", err)
	}
	if !updated.Status.GitHub.CompletedObserved {
		t.Fatal("expected CompletedObserved to be true")
	}
	if updated.Status.Phase != ghav1alpha1.EphemeralRunnerPhaseBusy {
		t.Fatalf("expected phase to remain Busy, got %s", updated.Status.Phase)
	}
}
