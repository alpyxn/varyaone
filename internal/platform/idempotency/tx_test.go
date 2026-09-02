package idempotency

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type reserveTxStub struct {
	execSQL   string
	querySQL  string
	inserted  bool
	queryStub pgx.Row
}

func (s *reserveTxStub) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	s.execSQL = sql
	if s.inserted {
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	return pgconn.NewCommandTag("INSERT 0 0"), nil
}

func (s *reserveTxStub) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	s.querySQL = sql
	return s.queryStub
}

type reserveRowStub struct{}

func (reserveRowStub) Scan(dest ...any) error {
	*dest[0].(*string) = "sales.invoice.post"
	*dest[1].(*string) = PayloadHash([]byte(`{"id":"invoice-1"}`))
	*dest[2].(*string) = "IN_PROGRESS"
	*dest[3].(**int) = nil
	*dest[4].(*[]byte) = nil
	return nil
}

type completedReserveRowStub struct{}

func (completedReserveRowStub) Scan(dest ...any) error {
	*dest[0].(*string) = "sales.invoice.post"
	*dest[1].(*string) = PayloadHash([]byte(`{"id":"invoice-1"}`))
	*dest[2].(*string) = "COMPLETED"
	status := 200
	*dest[3].(**int) = &status
	*dest[4].(*[]byte) = []byte(`{"document_id":"invoice-1"}`)
	return nil
}

func TestReserveTxFirstReservationIsNotReportedAsInProgress(t *testing.T) {
	stub := &reserveTxStub{inserted: true, queryStub: reserveRowStub{}}
	reservation, err := ReserveTx(
		context.Background(),
		stub,
		"company-1",
		"request-1",
		"sales.invoice.post",
		[]byte(`{"id":"invoice-1"}`),
		"user-1",
		"trace-1",
	)
	if err != nil {
		t.Fatalf("first reservation returned an error: %v", err)
	}
	if !reservation.Inserted || reservation.Completed {
		t.Fatalf("unexpected reservation: %+v", reservation)
	}
	if strings.Contains(stub.execSQL, "WITH inserted") {
		t.Fatal("reservation must not use a data-modifying CTE")
	}
	if !strings.Contains(stub.querySQL, "FOR UPDATE") {
		t.Fatal("reservation must lock the winning idempotency row")
	}
}

func TestReserveTxReplaysCompletedReservation(t *testing.T) {
	stub := &reserveTxStub{inserted: false, queryStub: completedReserveRowStub{}}
	reservation, err := ReserveTx(
		context.Background(),
		stub,
		"company-1",
		"request-1",
		"sales.invoice.post",
		[]byte(`{"id":"invoice-1"}`),
		"user-1",
		"trace-1",
	)
	if err != nil {
		t.Fatalf("completed reservation returned an error: %v", err)
	}
	if reservation.Inserted || !reservation.Completed || reservation.ResponseStatus != 200 {
		t.Fatalf("unexpected replay reservation: %+v", reservation)
	}
}
