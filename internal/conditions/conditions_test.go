package conditions

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSetCondition(t *testing.T) {
	var conds []metav1.Condition

	// 1. SetCondition with explicit reason
	SetCondition(&conds, TypeReady, metav1.ConditionTrue, ReasonReady, "Ready message")
	if !IsConditionTrue(conds, TypeReady) {
		t.Fatalf("expected Ready condition to be true")
	}

	cond := GetCondition(conds, TypeReady)
	if cond == nil || cond.Reason != ReasonReady || cond.Message != "Ready message" {
		t.Fatalf("unexpected condition values: %+v", cond)
	}

	// 2. Update condition
	SetCondition(&conds, TypeReady, metav1.ConditionFalse, ReasonNotReady, "Not ready")
	if IsConditionTrue(conds, TypeReady) {
		t.Fatalf("expected Ready condition to be false")
	}

	cond = GetCondition(conds, TypeReady)
	if cond == nil || cond.Reason != ReasonNotReady {
		t.Fatalf("expected reason %q, got %q", ReasonNotReady, cond.Reason)
	}

	// 3. SetCondition with empty reason (defaults to ReasonReady for ConditionTrue)
	SetCondition(&conds, TypeAPIReachable, metav1.ConditionTrue, "", "Reachable")
	cond = GetCondition(conds, TypeAPIReachable)
	if cond == nil || cond.Reason != ReasonReady {
		t.Errorf("expected default reason %s, got %v", ReasonReady, cond)
	}

	// 4. SetCondition with empty reason (defaults to ReasonPending for ConditionFalse)
	SetCondition(&conds, TypeMachinesReady, metav1.ConditionFalse, "", "Starting")
	cond = GetCondition(conds, TypeMachinesReady)
	if cond == nil || cond.Reason != ReasonPending {
		t.Errorf("expected default reason %s, got %v", ReasonPending, cond)
	}

	// 5. SetConditionWithGeneration
	SetConditionWithGeneration(&conds, 42, TypeCapacityReady, metav1.ConditionTrue, ReasonCapacitySufficient, "Sufficient")
	cond = GetCondition(conds, TypeCapacityReady)
	if cond == nil || cond.ObservedGeneration != 42 {
		t.Errorf("expected ObservedGeneration 42, got %v", cond)
	}

	// 6. RemoveCondition
	RemoveCondition(&conds, TypeCapacityReady)
	if GetCondition(conds, TypeCapacityReady) != nil {
		t.Errorf("expected TypeCapacityReady to be removed")
	}

	// 7. Nil handling
	SetCondition(nil, TypeReady, metav1.ConditionTrue, "", "")
	SetConditionWithGeneration(nil, 1, TypeReady, metav1.ConditionTrue, "", "")
	RemoveCondition(nil, TypeReady)
	if GetCondition(nil, TypeReady) != nil {
		t.Errorf("expected nil for nil slice")
	}
	if IsConditionTrue(nil, TypeReady) {
		t.Errorf("expected false for nil slice")
	}
}
