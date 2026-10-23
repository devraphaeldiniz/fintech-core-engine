# Fintech Core Engine (Livro-Razão Distribuído de Alta Performance)

[English Version](README.md) | Versão em Português

[![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat-square&logo=go)](https://golang.org)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16--Alpine-336791?style=flat-square&logo=postgresql)](https://www.postgresql.org)
[![Redis](https://img.shields.io/badge/Redis-7--Alpine-DC382D?style=flat-square&logo=redis)](https://redis.io)
[![Prometheus](https://img.shields.io/badge/Prometheus-Habilitado-E6522C?style=flat-square&logo=prometheus)](https://prometheus.io)
[![Grafana](https://img.shields.io/badge/Grafana-Pr%C3%A9--Configurado-F46800?style=flat-square&logo=grafana)](https://grafana.com)
[![License](https://img.shields.io/badge/License-MIT-blue.svg?style=flat-square)](LICENSE)

Motor bancário central (*core banking engine*) e livro-razão de partidas dobradas de alta vazão desenvolvido em Go. Projetado para garantir consistência transacional absoluta, ausência total de *race conditions* em saldos e conservação estrita de capital sob concorrência extrema.

---

## Visão Geral da Arquitetura

                  +---------------------------------------+
                  |       Cliente / Gateway de Pagamento  |
                  +---------------------------------------+
                                      |
                    HTTPS (REST + Chave API SHA-256)
                    Idempotency-Key: <UUID>
                                      v
                  +---------------------------------------+
                  |         Fintech Core API (Go)         |
                  |  - Middleware RBAC & Chaves SHA-256   |
                  |  - Camada de Deduplicação             |
                  +---------------------------------------+
                         |                         |
           Lock Distribuído (Spin + Backoff)       | Transação PostgreSQL
           TTL: 5s / Espera Máxima: 3s             | Isolamento: Read Committed
                         v                         v
               +------------------+      +-------------------+
               |   Cluster Redis  |      | PostgreSQL Engine |
               +------------------+      +-------------------+
                                         |  1. Lock Léxico   |
                                         |  2. Double-Entry  |
                                         |  3. Snapshot O(1) |
                                         |  4. Evento Outbox |
                                         +-------------------+
                                                   |
                                           Change Data Capture
                                           (Outbox Worker)
                                                   v
                                         +-------------------+
                                         | Tópicos Assíncronos|
                                         | (Webhooks/Kafka)  |
                                         +-------------------+

---

## Princípios de Engenharia Contábil e Invariantes

### 1. Livro-Razão por Partidas Dobradas (*Double-Entry Bookkeeping*)
Toda movimentação financeira requer um balanceamento exato entre débitos e créditos. O dinheiro não pode ser criado nem destruído:

$$\sum \text{Débitos} - \sum \text{Créditos} = 0$$

Todas as entradas do livro-razão são estritamente imutáveis (*append-only*). Operações de `UPDATE` ou `DELETE` são vedadas no nível de banco de dados.

### 2. Snapshots de Saldo $O(1)$ vs. Replay Histórico
Para assegurar tempos de resposta em milissegundos sem varredura linear de tabelas com milhões de linhas, os saldos consolidados residem na tabela `account_balances`:

* **Leitura:** Tempo constante $O(1)$ diretamente da tabela indexada de snapshots.
* **Escrita:** Atualização atômica do saldo consolidado dentro da mesma transação ACID que grava os lançamentos contábeis imutáveis.

### 3. Controle de Concorrência e Prevenção de Deadlock
Transferências simultâneas de alta frequência entre contas concorrentes (ex: Conta A transferindo para B enquanto B transfere para A) podem provocar travamento cíclico (*deadlock*). 

A engine adota uma estratégia de travas em dois níveis:

| Nível | Mecanismo | Escopo | Objetivo |
| :--- | :--- | :--- | :--- |
| **Nível 1 (Distribuído)** | Redis Lock / Spin-Lock com Jitter | Conta de Origem | Suaviza picos, evita *thundering herd* e enfileira requisições concorrentes antes de tocarem o banco. |
| **Nível 2 (Banco)** | `SELECT ... FOR UPDATE` Léxico | Transação SQL | Ordena os locks de contas de forma estritamente alfabética por UUID (`firstID < secondID`), eliminando matematicamente deadlocks cruzados (AB-BA). |

### 4. Transactional Outbox Pattern
Desacopla a liquidação financeira da entrega de eventos para serviços externos. Os eventos são registrados na tabela `outbox_events` na mesma transação ACID do ledger. Um worker em segundo plano consome os registros pendentes e realiza o despacho garantindo entrega (*at-least-once*), sem risco de inconsistência por falha de escrita dupla (*dual-write*).

### 5. Reconciliação Contábil Automática (*Nightly Audit*)
Um serviço de auditoria periódica comprova a integridade matemática do sistema avaliando:

$$\Delta = \text{account\_balances.current\_balance\_cents} - \sum (\text{CREDIT} - \text{DEBIT})$$

Se $\Delta \neq 0$, o sistema emite alerta crítico imediato e cataloga a divergência na tabela `audit_discrepancies`.

---

## Particionamento Mensal do Banco de Dados

A tabela `ledger_entries` é particionada declarativamente por intervalo de datas (`PARTITION BY RANGE (created_at)`):

* **Partições Mensais:** Cada mês armazena registros em tabelas isoladas (ex: `ledger_entries_2026_09`).
* **Partition Pruning:** Consultas filtram apenas a partição relevante, mantendo índices em memória RAM e evitando degradação sob bilhões de transações.
* **Provisionamento Autônomo:** Um worker em background roda rotinas PL/pgSQL que criam e mantêm provisionadas as partições para os próximos 12 meses de forma automática.

---

## Validação e Benchmark de Concorrência

A engine conta com uma suíte de teste de estresse concorrente (`cmd/stress/main.go`) simulando o cenário clássico de corrida bancária (*Bank Run*):

* **Saldo Inicial:** R$ 1.000,00 (100.000 centavos).
* **Carga:** 50 goroutines disparadas simultaneamente no mesmo nanossegundo.
* **Valor unitário:** R$ 30,00 (3.000 centavos) por transação.
* **Demanda Total:** R$ 1.500,00 (50% superior ao saldo existente).

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
Como Executar
Pré-requisitos
Docker Engine 24+ & Docker Compose v2+

Git

1. Inicializar o Ambiente
Clone o repositório e inicialize os containers do PostgreSQL, Redis, API, Prometheus e Grafana:

Bash
docker compose up -d --build
2. Verificar a Saúde da Engine
Bash
curl -s [http://127.0.0.1:8080/health](http://127.0.0.1:8080/health)
Retorno esperado:

JSON
{
  "engine": "enterprise-core-ledger",
  "status": "healthy",
  "version": "2.3.0"
}
3. Rodar o Teste de Carga Concorrente
Bash
MSYS_NO_PATHCONV=1 docker run --rm \
  --network fintech-core-engine_default \
  -e API_URL="http://fintech_api:8080" \
  -v "/$PWD:/app" \
  -w "/app" \
  golang:alpine \
  go run ./cmd/stress/main.go
4. Executar Reconciliação Contábil Sob Demanda
Bash
curl -s -X POST [http://127.0.0.1:8080/admin/reconciliation](http://127.0.0.1:8080/admin/reconciliation) \
  -H "X-API-Key: $API_KEY"
Observabilidade e Monitoramento
Prometheus: Métricas disponíveis em http://localhost:9090 (raspando /metrics a cada 5 segundos).

Grafana: Painel pronto em http://localhost:3000 (Usuário: admin / Senha: admin).

Métricas Expostas
fintech_transfers_total{status="settled|rejected", reason="..."}: Contador de liquidações e rejeições categorizadas.

fintech_transfer_duration_seconds: Histograma de latência transacional (percentis p50, p95 e p99).

fintech_ledger_entries_total{entry_type="CREDIT|DEBIT"}: Rastreamento em tempo real de débitos e créditos.

fintech_active_distributed_locks: Monitoramento de contenção de travas no Redis.

Referência da API
Todas as rotas operacionais exigem o cabeçalho X-API-Key. Rotas que alteram estado financeiro exigem X-Idempotency-Key.

Criar Conta
POST /accounts

Bash
curl -X POST [http://127.0.0.1:8080/accounts](http://127.0.0.1:8080/accounts) \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d '{
    "owner_name": "Raphael Diniz",
    "account_type": "CLIENT",
    "initial_balance_cents": 50000
  }'
Consultar Saldo em Tempo Real
GET /accounts/{id}/balance

Bash
curl -X GET [http://127.0.0.1:8080/accounts/](http://127.0.0.1:8080/accounts/){account_id}/balance \
  -H "X-API-Key: $API_KEY"
Executar Transferência Atômica
POST /transfers

Bash
curl -X POST [http://127.0.0.1:8080/transfers](http://127.0.0.1:8080/transfers) \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -H "X-Idempotency-Key: transfer-uuid-1234" \
  -d '{
    "from_account_id": "834164b3-b541-4547-8a6a-5c2132d731e0",
    "to_account_id": "f516a70a-4424-42b7-a3f2-1d54d24a0d9e",
    "amount_cents": 15000,
    "description": "Liquidação Pix"
  }'
