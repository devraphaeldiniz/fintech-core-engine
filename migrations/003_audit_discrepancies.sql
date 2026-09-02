CREATE TABLE IF NOT EXISTS audit_discrepancies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id),
    snapshot_balance_cents BIGINT NOT NULL,
    ledger_sum_cents BIGINT NOT NULL,
    discrepancy_cents BIGINT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'OPEN', -- OPEN, INVESTIGATING, RESOLVED
    detected_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_open ON audit_discrepancies(status, detected_at);
