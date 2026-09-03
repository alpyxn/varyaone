-- Payroll settles employee cash advances through a dedicated ledger transaction
-- type. Deducting an advance on a payslip previously left the advance sub-ledger
-- untouched: the advance stayed OPEN, could be deducted again the following
-- month, and every outstanding-balance report was wrong.
--
-- PAYROLL_DEDUCTION carries no finance movement and no account: the cash effect
-- is already captured by the payroll payment, whose net was reduced by exactly
-- this amount. Recording a finance movement here would double-count the cash.

ALTER TABLE employee_advance_transactions
  DROP CONSTRAINT employee_advance_transactions_transaction_type_check;

ALTER TABLE employee_advance_transactions
  ADD CONSTRAINT employee_advance_transactions_transaction_type_check
  CHECK (transaction_type = ANY (ARRAY['DISBURSEMENT'::text, 'REPAYMENT'::text, 'PAYROLL_DEDUCTION'::text, 'WRITE_OFF'::text, 'REVERSAL'::text]));

ALTER TABLE employee_advance_transactions
  DROP CONSTRAINT employee_advance_transactions_check;

ALTER TABLE employee_advance_transactions
  ADD CONSTRAINT employee_advance_transactions_check CHECK (
    ((transaction_type = ANY (ARRAY['WRITE_OFF'::text, 'PAYROLL_DEDUCTION'::text]))
       AND (account_id IS NULL) AND (finance_movement_id IS NULL))
    OR ((transaction_type = ANY (ARRAY['DISBURSEMENT'::text, 'REPAYMENT'::text]))
       AND (account_id IS NOT NULL) AND (finance_movement_id IS NOT NULL))
    OR ((transaction_type = 'REVERSAL'::text) AND ((account_id IS NULL) = (finance_movement_id IS NULL)))
  );
