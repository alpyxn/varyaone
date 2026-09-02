package inventory

import "errors"

const (
	TransferTypeQuick    = "QUICK"
	TransferTypeWorkflow = "WORKFLOW"

	TransferDraft             = "DRAFT"
	TransferRequested         = "REQUESTED"
	TransferApproved          = "APPROVED"
	TransferInTransit         = "IN_TRANSIT"
	TransferPartiallyReceived = "PARTIALLY_RECEIVED"
	TransferReceived          = "RECEIVED"
	TransferCancelled         = "CANCELLED"

	CountDraft      = "DRAFT"
	CountInProgress = "IN_PROGRESS"
	Counted         = "COUNTED"
	CountReview     = "REVIEW"
	CountPosted     = "POSTED"
	CountCancelled  = "CANCELLED"
)

func validTransferType(value string) bool {
	switch value {
	case TransferTypeQuick, TransferTypeWorkflow:
		return true
	default:
		return false
	}
}

func validTransferState(value string) bool {
	switch value {
	case TransferDraft, TransferRequested, TransferApproved, TransferInTransit,
		TransferPartiallyReceived, TransferReceived, TransferCancelled:
		return true
	default:
		return false
	}
}

func CanTransitionTransfer(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case TransferDraft:
		// The detail-card flow may approve a new transfer directly. REQUESTED
		// remains valid for historical/API-created rows.
		return to == TransferRequested || to == TransferApproved || to == TransferCancelled
	case TransferRequested:
		return to == TransferApproved || to == TransferCancelled
	case TransferApproved:
		return to == TransferInTransit || to == TransferCancelled
	case TransferInTransit:
		return to == TransferPartiallyReceived || to == TransferReceived || to == TransferCancelled
	case TransferPartiallyReceived:
		return to == TransferReceived || to == TransferCancelled
	default:
		return false
	}
}

func ValidateTransferTransition(from, to string) error {
	if !CanTransitionTransfer(from, to) {
		return codeError(ErrWarehouseTransferInvalidState.Error(), ErrWarehouseTransferInvalidState, "%s -> %s geçişi geçersiz", from, to)
	}
	return nil
}

func CanTransitionCount(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case CountDraft:
		return to == CountInProgress
	case CountInProgress:
		return to == Counted || to == CountReview || to == CountCancelled
	case Counted:
		return to == CountReview || to == CountPosted || to == CountCancelled
	case CountReview:
		return to == CountPosted || to == CountCancelled
	default:
		return false
	}
}

func ValidateCountTransition(from, to string) error {
	if !CanTransitionCount(from, to) {
		return errors.New("invalid stock count state transition")
	}
	return nil
}

// CanTransitionStockCountEngine describes the state machine used by the
// append-only stock count engine. It is separate from CanTransitionCount
// because the older mutable count aggregate still has its own lifecycle.
func CanTransitionStockCountEngine(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case StockCountEngineInProgress:
		return to == StockCountEngineReview || to == StockCountEngineCancelled
	case StockCountEngineReview:
		return to == StockCountEngineInProgress || to == StockCountEnginePosted || to == StockCountEngineCancelled
	default:
		return false
	}
}
