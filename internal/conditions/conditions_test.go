package conditions

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSetCondition(t *testing.T) {
	var conds []metav1.Condition

	SetCondition(&conds, TypeReady, metav1.ConditionTrue, ReasonReady, "Ready message")
	if !IsConditionTrue(conds, TypeReady) {
		t.Fatalf("expected Ready condition to be true")
	}

	cond := GetCondition(conds, TypeReady)
	if cond == nil || cond.Reason != ReasonReady || cond.Message != "Ready message" {
		t.Fatalf("unexpected condition values: %+v", cond)
	}

	// Update condition
	SetCondition(&conds, TypeReady, metav1.ConditionFalse, ReasonNotReady, "Not ready")
	if IsConditionTrue(conds, TypeReady) {
		t.Fatalf("expected Ready condition to be false")
	}

	cond = GetCondition(conds, TypeReady)
	if cond == nil || cond.Reason != ReasonNotReady {
		t.Fatalf("expected reason %q, got %q", ReasonNotReady, cond.Reason)
	}
}
