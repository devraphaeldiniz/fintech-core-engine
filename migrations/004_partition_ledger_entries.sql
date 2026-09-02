BEGIN;

-- 1. Renomear tabela atual e remover constraints conflitantes
ALTER TABLE ledger_entries RENAME TO ledger_entries_old;
ALTER TABLE ledger_entries_old DROP CONSTRAINT IF EXISTS fk_ledger_account;
ALTER TABLE ledger_entries_old DROP CONSTRAINT IF EXISTS fk_ledger_transaction;

-- 2. Criar a nova tabela mãe particionada por RANGE (created_at)
CREATE TABLE ledger_entries (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    transaction_id UUID NOT NULL,
    account_id UUID NOT NULL,
    entry_type VARCHAR(10) NOT NULL CHECK (entry_type IN ('CREDIT', 'DEBIT')),
    amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- 3. Criar partições mensais (2026 e 2027)
CREATE TABLE ledger_entries_2026_08 PARTITION OF ledger_entries
    FOR VALUES FROM ('2026-08-01 00:00:00+00') TO ('2026-09-01 00:00:00+00');

CREATE TABLE ledger_entries_2026_09 PARTITION OF ledger_entries
    FOR VALUES FROM ('2026-09-01 00:00:00+00') TO ('2026-10-01 00:00:00+00');

CREATE TABLE ledger_entries_2026_10 PARTITION OF ledger_entries
    FOR VALUES FROM ('2026-10-01 00:00:00+00') TO ('2026-11-01 00:00:00+00');

CREATE TABLE ledger_entries_2026_11 PARTITION OF ledger_entries
    FOR VALUES FROM ('2026-11-01 00:00:00+00') TO ('2026-12-01 00:00:00+00');

CREATE TABLE ledger_entries_2026_12 PARTITION OF ledger_entries
    FOR VALUES FROM ('2026-12-01 00:00:00+00') TO ('2027-01-01 00:00:00+00');

CREATE TABLE ledger_entries_2027_01 PARTITION OF ledger_entries
    FOR VALUES FROM ('2027-01-01 00:00:00+00') TO ('2027-02-01 00:00:00+00');

-- Partição padrão (guarda lançamentos fora das faixas mapeadas)
CREATE TABLE ledger_entries_default PARTITION OF ledger_entries DEFAULT;

-- 4. Criar índices locais nas partições para busca rápida
CREATE INDEX idx_ledger_account_created ON ledger_entries (account_id, created_at DESC);
CREATE INDEX idx_ledger_transaction ON ledger_entries (transaction_id);

-- 5. Migrar os registros legados para a nova estrutura
INSERT INTO ledger_entries (id, transaction_id, account_id, entry_type, amount_cents, created_at)
SELECT id, transaction_id, account_id, entry_type, amount_cents, created_at
FROM ledger_entries_old;

-- 6. Limpar tabela antiga
DROP TABLE ledger_entries_old;

COMMIT;
