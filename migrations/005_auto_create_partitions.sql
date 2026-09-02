CREATE OR REPLACE FUNCTION maintain_ledger_partitions(months_ahead INT DEFAULT 12)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    start_date DATE;
    end_date DATE;
    partition_name TEXT;
    sql_stmt TEXT;
    i INT;
BEGIN
    -- Começa no primeiro dia do mês corrente
    start_date := date_trunc('month', CURRENT_DATE);

    FOR i IN 0..months_ahead LOOP
        end_date := start_date + INTERVAL '1 month';
        partition_name := 'ledger_entries_' || to_char(start_date, 'YYYY_MM');

        -- Monta o DDL declarativo da partição
        sql_stmt := format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF ledger_entries ' ||
            'FOR VALUES FROM (%L) TO (%L);',
            partition_name,
            start_date,
            end_date
        );

        EXECUTE sql_stmt;
        
        -- Avança para o próximo mês
        start_date := end_date;
    END LOOP;
END;
$$;
