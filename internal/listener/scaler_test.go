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

	machine := &ghav1alpha1.RunnerMachine{}
	machine.Name = "m1"
	machine.Namespace = "default"

	four := int32(4)
	scaleSet := &ghav1alpha1.RunnerScaleSet{}
	scaleSet.Name = "test-ss"
	scaleSet.Namespace = "default"
	scaleSet.Spec.NodePoolRef.Name = "pool-1"
	scaleSet.Spec.Scaling.MinRunners = 0
	scaleSet.Spec.Scaling.MaxRunners = &four
	scaleSet.Status.EffectiveMaxRunners = 4

	zero := int32(0)
	ers := &ghav1alpha1.EphemeralRunnerSet{}
	ers.Name = "test-ss"
	ers.Namespace = "default"
	ers.Spec.ScaleSetRef.Name = "test-ss"
	ers.Spec.Replicas = &zero

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nodePool, machine, scaleSet, ers).WithStatusSubresource(scaleSet, ers).Build()
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

	if updated.Status.GitHub.AssignedJobs != 2 {
		t.Fatalf("expected AssignedJobs 2, got %d", updated.Status.GitHub.AssignedJobs)
	}
	if !updated.Status.Listener.Ready {
		t.Fatal("expected Listener.Ready to be true")
	}
	if !tracker.initialStatisticsReceived {
		t.Fatal("expected initialStatisticsReceived to be true")
	}

	var updatedERS ghav1alpha1.EphemeralRunnerSet
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-ss"}, &updatedERS); err != nil {
		t.Fatalf("failed to get updated ERS: %v", err)
	}
	if updatedERS.Spec.Replicas == nil || *updatedERS.Spec.Replicas != 2 {
		t.Fatalf("expected ERS replicas 2, got %v", updatedERS.Spec.Replicas)
	}

	// MaxRunners (4) を超える要求 (例えば6) が来た場合、4にcapされることを検証
	_, err = scaler.HandleDesiredRunnerCount(context.Background(), 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-ss"}, &updatedERS); err != nil {
		t.Fatalf("failed to get updated ERS: %v", err)
	}
	if updatedERS.Spec.Replicas == nil || *updatedERS.Spec.Replicas != 4 {
		t.Fatalf("expected ERS replicas to be capped at 4, got %v", updatedERS.Spec.Replicas)
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
	jobStarted.JobID = "100"

	if err := scaler.HandleJobStarted(context.Background(), jobStarted); err != nil {
		t.Fatalf("unexpected error on job started: %v", err)
	}

	var updatedRunner ghav1alpha1.EphemeralRunner
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "runner-1"}, &updatedRunner); err != nil {
		t.Fatalf("failed to get runner: %v", err)
	}

	if updatedRunner.Status.Phase != ghav1alpha1.EphemeralRunnerPhaseBusy {
		t.Errorf("expected phase Busy, got %v", updatedRunner.Status.Phase)
	}
	if updatedRunner.Status.GitHub.RunnerID != 12345 {
		t.Errorf("expected runnerID 12345, got %d", updatedRunner.Status.GitHub.RunnerID)
	}
	if updatedRunner.Status.GitHub.JobID != 100 {
		t.Errorf("expected jobID 100, got %d", updatedRunner.Status.GitHub.JobID)
	}

	jobCompleted := &scaleset.JobCompleted{}
	jobCompleted.RunnerName = "runner-1"
	jobCompleted.Result = "success"

	if err := scaler.HandleJobCompleted(context.Background(), jobCompleted); err != nil {
		t.Fatalf("unexpected error on job completed: %v", err)
	}

	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "runner-1"}, &updatedRunner); err != nil {
		t.Fatalf("failed to get runner: %v", err)
	}

	if updatedRunner.Status.Phase != ghav1alpha1.EphemeralRunnerPhaseCompleted {
		t.Errorf("expected phase Completed, got %v", updatedRunner.Status.Phase)
	}
	if !updatedRunner.Status.GitHub.CompletedObserved {
		t.Errorf("expected completedObserved true, got %v", updatedRunner.Status.GitHub.CompletedObserved)
	}
}
