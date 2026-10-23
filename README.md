# Fintech Core Engine (High-Performance Distributed Ledger)

English Version | [Versão em Português](README.pt-BR.md)

[![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat-square&logo=go)](https://golang.org)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16--Alpine-336791?style=flat-square&logo=postgresql)](https://www.postgresql.org)
[![Redis](https://img.shields.io/badge/Redis-7--Alpine-DC382D?style=flat-square&logo=redis)](https://redis.io)
[![Prometheus](https://img.shields.io/badge/Prometheus-Enabled-E6522C?style=flat-square&logo=prometheus)](https://prometheus.io)
[![Grafana](https://img.shields.io/badge/Grafana-Pre--Provisioned-F46800?style=flat-square&logo=grafana)](https://grafana.com)
[![License](https://img.shields.io/badge/License-MIT-blue.svg?style=flat-square)](LICENSE)

An enterprise-grade, high-throughput core banking engine and double-entry ledger designed in Go. Built to enforce absolute transactional consistency, zero balance race conditions, and capital conservation under intense concurrent loads.

---

## Architecture Overview

                  +---------------------------------------+
                  |       Client / Payment Gateway        |
                  +---------------------------------------+
                                      |
                    HTTPS (REST + SHA-256 API Key)
                    Idempotency-Key: <UUID>
                                      v
                  +---------------------------------------+
                  |         Fintech Core API (Go)         |
                  |  - RBAC & SHA-256 Key Middleware      |
                  |  - Request Deduplication Layer        |
                  +---------------------------------------+
                         |                         |
           Distributed Lock (Spin + Backoff)       | PostgreSQL Transaction
           TTL: 5s / Max Wait: 3s                  | Isolation: Read Committed
                         v                         v
               +------------------+      +-------------------+
               |   Redis Cluster  |      | PostgreSQL Engine |
               +------------------+      +-------------------+
                                         |  1. Lexical Lock  |
                                         |  2. Double-Entry  |
                                         |  3. Snapshot O(1) |
                                         |  4. Outbox Event  |
                                         +-------------------+
                                                   |
                                           Change Data Capture
                                           (Outbox Worker)
                                                   v
                                         +-------------------+
                                         | Downstream Topics |
                                         | (Webhooks/Kafka)  |
                                         +-------------------+

---

## Core Financial Design & Invariants

### 1. Double-Entry Bookkeeping
Every monetary movement requires an exact balance of credits and debits across accounts. Money cannot be created or destroyed during a transfer:

$$\sum \text{Debits} - \sum \text{Credits} = 0$$

All ledger transactions are strictly append-only. Mutation or deletion of entries is prohibited at the database engine level.

### 2. $O(1)$ Balance Snapshots vs. Historical Replay
To maintain millisecond response times without executing unbounded table scans across millions of ledger rows, balances are maintained in a dedicated snapshot table (`account_balances`).

* **Read Operations:** $O(1)$ constant time lookup directly from indexed snapshot balances.
* **Write Operations:** Atomic update of snapshot records within the same transaction that inserts append-only ledger entries.

### 3. Concurrency Control & Deadlock Avoidance
High-frequency concurrent transfers between accounts can trigger cyclic lock dependencies in relational engines. 

The core implements a two-tier locking strategy:

| Layer | Mechanism | Scope | Purpose |
| :--- | :--- | :--- | :--- |
| **Tier 1 (Distributed)** | Redis Redlock / Spin-Lock with Jitter | Source Account | Smooths traffic spikes, prevents lock contention storms, and queues competing callers. |
| **Tier 2 (Database)** | Lexicographical `SELECT ... FOR UPDATE` | Transaction-level | Orders account locks strictly by UUID (`firstID < secondID`), eliminating cyclical wait conditions (AB-BA deadlocks). |

### 4. Transactional Outbox Pattern
Decouples payment processing from external event publication. Events are written to an `outbox_events` table inside the same ACID database transaction as the financial ledger. A background worker picks up `PENDING` events and dispatches them with retry guarantees, preventing dual-write inconsistencies.

### 5. Nightly Accounting Reconciliation
An administrative audit process validates data integrity across the system by continuously evaluating:

$$\Delta = \text{account\_balances.current\_balance\_cents} - \sum (\text{CREDIT} - \text{DEBIT})$$

Any account with $\Delta \neq 0$ is flagged immediately in `audit_discrepancies` for operational inspection.

---

## Database Partitioning

The `ledger_entries` table is declaratively partitioned by date range (`PARTITION BY RANGE (created_at)`):

* **Monthly Partitions:** Each month stores records in dedicated tables (e.g., `ledger_entries_2026_09`).
* **Partition Pruning:** Historical queries query only the target month's physical table, keeping index operations in RAM and eliminating linear disk read degradation.
* **Autonomous Provisioning:** A background partition worker inspects the calendar and provisions tables for the upcoming 12 months in advance via PL/pgSQL routines.

---

## Benchmark & Concurrency Validation

The engine includes an automated concurrent stress suite (`cmd/stress/main.go`) simulating a bank run scenario:

* **Initial Balance:** R$ 1,000.00 (100,000 cents).
* **Workload:** 50 goroutines launched simultaneously at the same nanosecond.
* **Transfer Amount:** R$ 30.00 (3,000 cents) per operation.
* **Total Demand:** R$ 1,500.00 (exceeds balance by 50%).

```text
==================================================
   FINTECH CORE ENGINE - TESTE DE CARGA CONCORRENTE
==================================================
[SETUP] Conta Origem criada: ad43f92b... | Saldo: R$ 1000.00
[SETUP] Conta Destino criada: d6ec5c8c... | Saldo: R$ 0.00
--------------------------------------------------
[DISPARO] 50 requisições paralelas de R$ 30.00...
[DEMANDA] Total Demandado: R$ 1500.00 | Saldo: R$ 1000.00
--------------------------------------------------
Tempo Total Decorrido: 231.05ms
Throughput Médio     : 216.40 req/seg
Transferências com SUCESSO: 33 (Esperado: 33)
Transferências REJEITADAS : 17 (Esperado: 17 por saldo/lock)
--------------------------------------------------
             CONFERÊNCIA DE BALANÇOS              
--------------------------------------------------
Saldo Final Origem : R$ 10.00 (Centavos: 1000)
Saldo Final Destino: R$ 990.00 (Centavos: 99000)
Soma dos Balanços  : R$ 1000.00 (Esperado: R$ 1.000,00)
==================================================
✓ SUCESSO: Zero race conditions detectadas!
✓ Conservação de capital e travas atômicas 100% validadas.
==================================================
Quickstart
Prerequisites
Docker Engine 24+ & Docker Compose v2+

Git

1. Boot Environment
Clone the repository and spin up PostgreSQL, Redis, API, Prometheus, and Grafana:

Bash
docker compose up -d --build
2. Verify System Health
Bash
curl -s [http://127.0.0.1:8080/health](http://127.0.0.1:8080/health)
Expected output:

JSON
{
  "engine": "enterprise-core-ledger",
  "status": "healthy",
  "version": "2.3.0"
}
3. Run the Concurrency Stress Test
Bash
MSYS_NO_PATHCONV=1 docker run --rm \
  --network fintech-core-engine_default \
  -e API_URL="http://fintech_api:8080" \
  -v "/$PWD:/app" \
  -w "/app" \
  golang:alpine \
  go run ./cmd/stress/main.go
4. Trigger Nightly Audit
Bash
curl -s -X POST [http://127.0.0.1:8080/admin/reconciliation](http://127.0.0.1:8080/admin/reconciliation) \
  -H "X-API-Key: <sua-chave-de-desenvolvimento>"
Observability & Dashboards
Prometheus: Metrics available at http://localhost:9090 (scraping /metrics every 5 seconds).

Grafana: Pre-configured dashboards at http://localhost:3000 (User: admin / Pass: admin).

Key Metrics Monitored
fintech_transfers_total{status="settled|rejected", reason="..."}: Counter tracking state changes.

fintech_transfer_duration_seconds: Histogram tracking settlement latency percentiles (p50, p95, p99).

fintech_ledger_entries_total{entry_type="CREDIT|DEBIT"}: Real-time ledger debit/credit matching.

fintech_active_distributed_locks: Real-time Redis lock acquisition and release gauge.

API Reference
All protected endpoints require the X-API-Key header. State-modifying requests require X-Idempotency-Key.

Create Account
POST /accounts

Bash
curl -X POST [http://127.0.0.1:8080/accounts](http://127.0.0.1:8080/accounts) \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <sua-chave-de-desenvolvimento>" \
  -d '{
    "owner_name": "Raphael Diniz",
    "account_type": "CLIENT",
    "initial_balance_cents": 50000
  }'
Get Account Balance
GET /accounts/{id}/balance

Bash
curl -X GET [http://127.0.0.1:8080/accounts/](http://127.0.0.1:8080/accounts/){account_id}/balance \
  -H "X-API-Key: <sua-chave-de-desenvolvimento>"
Execute Transfer
POST /transfers

Bash
curl -X POST [http://127.0.0.1:8080/transfers](http://127.0.0.1:8080/transfers) \
  -H "Content-Type: application/json" \
  -H "X-API-Key: <sua-chave-de-desenvolvimento>" \
  -H "X-Idempotency-Key: transfer-uuid-1234" \
  -d '{
    "from_account_id": "834164b3-b541-4547-8a6a-5c2132d731e0",
    "to_account_id": "f516a70a-4424-42b7-a3f2-1d54d24a0d9e",
    "amount_cents": 15000,
    "description": "Payment Settlement"
  }'
