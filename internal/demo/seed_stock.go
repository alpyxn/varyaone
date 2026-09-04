package demo

import (
	"context"
	"fmt"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/inventory"
)

// seedStockOperations fills the warehouse screens: stock moving between the two
// locations in each of the states a transfer can be in, and the manual
// movements every warehouse ends up making - a damaged unit written off and a
// count difference booked in.
func (r *Runner) seedStockOperations(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	if err := r.seedTransfers(ctx, session, svc, built); err != nil {
		return err
	}
	return r.seedManualMovements(ctx, session, svc, built)
}

func (r *Runner) seedTransfers(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	plain := built.plainProducts()
	tracked := built.variantProducts()

	// A quick transfer: shipped and received in one step, the way a van run
	// between two of your own depots is actually recorded.
	if _, err := svc.inventory.CreateTransfer(ctx, inventory.TransferInput{
		CompanyID: CompanyID, TransferType: inventory.TransferTypeQuick,
		SourceWarehouseID: built.scope.warehouseID, DestinationWarehouseID: built.scope.storeID,
		RequestedBy: session.User.ID, IdempotencyKey: "demo-seed:transfer-quick",
		Lines: []inventory.TransferLineInput{
			{ProductID: plain[0].ID, Quantity: "6"},
			{ProductID: plain[1].ID, Quantity: "4"},
		},
	}); err != nil {
		return err
	}

	// A workflow transfer that has been received in full.
	shipped, err := svc.inventory.CreateTransfer(ctx, inventory.TransferInput{
		CompanyID: CompanyID, TransferType: inventory.TransferTypeWorkflow,
		SourceWarehouseID: built.scope.warehouseID, DestinationWarehouseID: built.scope.storeID,
		RequestedBy: session.User.ID, IdempotencyKey: "demo-seed:transfer-received",
		Lines: []inventory.TransferLineInput{
			{ProductID: tracked[0].ID, VariantID: tracked[0].variantID(0), Quantity: "5"},
			{ProductID: tracked[0].ID, VariantID: tracked[0].variantID(1), Quantity: "3"},
		},
	})
	if err != nil {
		return err
	}
	receives := make([]inventory.ReceiveLineInput, 0, len(shipped.Lines))
	for _, line := range shipped.Lines {
		receives = append(receives, inventory.ReceiveLineInput{LineID: line.ID, ReceivedQuantity: line.Quantity})
	}
	if _, err = svc.inventory.ReceiveTransferWithKey(ctx, CompanyID, shipped.ID, session.User.ID, shipped.Version, receives, "demo-seed:transfer-receive"); err != nil {
		return err
	}

	// And one still on the road: creating a workflow transfer ships the source
	// quantity, so this one sits in transit until somebody receives it.
	_, err = svc.inventory.CreateTransfer(ctx, inventory.TransferInput{
		CompanyID: CompanyID, TransferType: inventory.TransferTypeWorkflow,
		SourceWarehouseID: built.scope.warehouseID, DestinationWarehouseID: built.scope.storeID,
		RequestedBy: session.User.ID, IdempotencyKey: "demo-seed:transfer-in-transit",
		Lines: []inventory.TransferLineInput{
			{ProductID: plain[3].ID, Quantity: "8"},
			{ProductID: tracked[1].ID, VariantID: tracked[1].variantID(2), Quantity: "2"},
		},
	})
	return err
}

func (r *Runner) seedManualMovements(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	plain := built.plainProducts()
	tracked := built.variantProducts()
	operations := []inventory.StockMovementOperationInput{{
		CompanyID: CompanyID, WarehouseID: built.scope.warehouseID, ProductID: tracked[2].ID,
		Direction: "OUT", ReasonCode: "DAMAGE", ReasonDescription: "Nakliyede hasar gördü",
		UnitCode: tracked[2].unit(), IdempotencyKey: "demo-seed:movement-damage",
		ActorUserID: session.User.ID,
		Lines: []inventory.StockMovementOperationLine{
			{VariantID: tracked[2].variantID(0), Quantity: "2"},
		},
	}}
	for index, operation := range operations {
		if _, err := svc.inventory.PostStockMovementOperation(ctx, operation); err != nil {
			return fmt.Errorf("manual movement %d: %w", index, err)
		}
	}
	// A count difference on a product without variants. The multi-variant
	// operation command above only accepts variant lines, so a plain product is
	// booked through the single-movement primitive.
	_, err := svc.inventory.PostStockMovement(ctx, inventory.MovementInput{
		CompanyID: CompanyID, WarehouseID: built.scope.storeID, ProductID: plain[6].ID,
		MovementType: inventory.MovementManualAdjustment, Direction: "IN", Quantity: "3",
		UnitCode: plain[6].unit(), ReasonCode: "CORRECTION", ReasonDescription: "Sayım fazlası",
		UnitCost: plain[6].spec.purchase, Currency: "TRY",
		IdempotencyKey: "demo-seed:movement-count-difference", ActorUserID: session.User.ID,
	})
	return err
}
