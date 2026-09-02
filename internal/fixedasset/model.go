// Package fixedasset owns operational asset cards and employee custody history.
package fixedasset

import (
	"errors"
	"time"
)

var (
	ErrAssetUnavailable       = errors.New("FIXED_ASSET_NOT_AVAILABLE")
	ErrActiveAssignmentExists = errors.New("FIXED_ASSET_ALREADY_ASSIGNED")
	ErrAssignmentReturned     = errors.New("FIXED_ASSET_ASSIGNMENT_RETURNED")
	ErrReturnBeforeAssignment = errors.New("FIXED_ASSET_INVALID_RETURN_DATE")
)

type Asset struct {
	ID        string
	CompanyID string
	Status    string
	Version   int64
}

type Assignment struct {
	ID         string
	CompanyID  string
	AssetID    string
	EmployeeID string
	AssignedAt time.Time
	ReturnedAt *time.Time
}

func Assign(asset Asset, hasActiveAssignment bool, assignment Assignment) (Asset, Assignment, error) {
	if asset.Status != "AVAILABLE" {
		return Asset{}, Assignment{}, ErrAssetUnavailable
	}
	if hasActiveAssignment {
		return Asset{}, Assignment{}, ErrActiveAssignmentExists
	}
	if asset.ID == "" || asset.CompanyID == "" || assignment.AssetID != asset.ID || assignment.CompanyID != asset.CompanyID || assignment.EmployeeID == "" || assignment.AssignedAt.IsZero() {
		return Asset{}, Assignment{}, ErrAssetUnavailable
	}
	asset.Status = "ASSIGNED"
	asset.Version++
	return asset, assignment, nil
}

func Return(asset Asset, assignment Assignment, returnedAt time.Time) (Asset, Assignment, error) {
	if assignment.ReturnedAt != nil {
		return Asset{}, Assignment{}, ErrAssignmentReturned
	}
	if returnedAt.Before(assignment.AssignedAt) {
		return Asset{}, Assignment{}, ErrReturnBeforeAssignment
	}
	assignment.ReturnedAt = &returnedAt
	asset.Status = "AVAILABLE"
	asset.Version++
	return asset, assignment, nil
}
