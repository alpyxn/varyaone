package fixedasset

import (
	"testing"
	"time"
)

func TestAssignmentLifecycleCannotReturnTwice(t *testing.T) {
	now := time.Now().UTC()
	asset := Asset{ID: "a", CompanyID: "c", Status: "AVAILABLE", Version: 1}
	assignment := Assignment{ID: "x", CompanyID: "c", AssetID: "a", EmployeeID: "e", AssignedAt: now}
	asset, assignment, err := Assign(asset, false, assignment)
	if err != nil || asset.Status != "ASSIGNED" {
		t.Fatalf("assign=%+v %v", asset, err)
	}
	asset, assignment, err = Return(asset, assignment, now.Add(time.Hour))
	if err != nil || asset.Status != "AVAILABLE" {
		t.Fatalf("return=%+v %v", asset, err)
	}
	if _, _, err = Return(asset, assignment, now.Add(2*time.Hour)); err != ErrAssignmentReturned {
		t.Fatalf("second return=%v", err)
	}
}
