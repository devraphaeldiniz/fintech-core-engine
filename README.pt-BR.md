# Fintech Core Engine (Livro-RazÃ£o DistribuÃ­do de Alta Performance)

[English Version](README.md) | VersÃ£o em PortuguÃªs

[![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=flat-square&logo=go)](https://golang.org)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16--Alpine-336791?style=flat-square&logo=postgresql)](https://www.postgresql.org)
[![Redis](https://img.shields.io/badge/Redis-7--Alpine-DC382D?style=flat-square&logo=redis)](https://redis.io)
[![Prometheus](https://img.shields.io/badge/Prometheus-Habilitado-E6522C?style=flat-square&logo=prometheus)](https://prometheus.io)
[![Grafana](https://img.shields.io/badge/Grafana-Pr%C3%A9--Configurado-F46800?style=flat-square&logo=grafana)](https://grafana.com)
[![License](https://img.shields.io/badge/License-MIT-blue.svg?style=flat-square)](LICENSE)

Motor bancÃ¡rio central (*core banking engine*) e livro-razÃ£o de partidas dobradas de alta vazÃ£o desenvolvido em Go. Projetado para garantir consistÃªncia transacional absoluta, ausÃªncia total de *race conditions* em saldos e conservaÃ§Ã£o estrita de capital sob concorrÃªncia extrema.

---

## VisÃ£o Geral da Arquitetura

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
                  |  - Camada de DeduplicaÃ§Ã£o             |
                  +---------------------------------------+
                         |                         |
           Lock DistribuÃ­do (Spin + Backoff)       | TransaÃ§Ã£o PostgreSQL
           TTL: 5s / Espera MÃ¡xima: 3s             | Isolamento: Read Committed
                         v                         v
               +------------------+      +-------------------+
               |   Cluster Redis  |      | PostgreSQL Engine |
               +------------------+      +-------------------+
                                         |  1. Lock LÃ©xico   |
                                         |  2. Double-Entry  |
                                         |  3. Snapshot O(1) |
                                         |  4. Evento Outbox |
                                         +-------------------+
                                                   |
                                           Change Data Capture
                                           (Outbox Worker)
                                                   v
                                         +-------------------+
                                         | TÃ³picos AssÃ­ncronos|
                                         | (Webhooks/Kafka)  |
                                         +-------------------+

---

## PrincÃ­pios de Engenharia ContÃ¡bil e Invariantes

### 1. Livro-RazÃ£o por Partidas Dobradas (*Double-Entry Bookkeeping*)
Toda movimentaÃ§Ã£o financeira requer um balanceamento exato entre dÃ©bitos e crÃ©ditos. O dinheiro nÃ£o pode ser criado nem destruÃ­do:

$$\sum \text{DÃ©bitos} - \sum \text{CrÃ©ditos} = 0$$

Todas as entradas do livro-razÃ£o sÃ£o estritamente imutÃ¡veis (*append-only*). OperaÃ§Ãµes de `UPDATE` ou `DELETE` sÃ£o vedadas no nÃ­vel de banco de dados.

### 2. Snapshots de Saldo $O(1)$ vs. Replay HistÃ³rico
Para assegurar tempos de resposta em milissegundos sem varredura linear de tabelas com milhÃµes de linhas, os saldos consolidados residem na tabela `account_balances`:

* **Leitura:** Tempo constante $O(1)$ diretamente da tabela indexada de snapshots.
* **Escrita:** AtualizaÃ§Ã£o atÃ´mica do saldo consolidado dentro da mesma transaÃ§Ã£o ACID que grava os lanÃ§amentos contÃ¡beis imutÃ¡veis.

### 3. Controle de ConcorrÃªncia e PrevenÃ§Ã£o de Deadlock
TransferÃªncias simultÃ¢neas de alta frequÃªncia entre contas concorrentes (ex: Conta A transferindo para B enquanto B transfere para A) podem provocar travamento cÃ­clico (*deadlock*). 

A engine adota uma estratÃ©gia de travas em dois nÃ­veis:

| NÃ­vel | Mecanismo | Escopo | Objetivo |
| :--- | :--- | :--- | :--- |
| **NÃ­vel 1 (DistribuÃ­do)** | Redis Lock / Spin-Lock com Jitter | Conta de Origem | Suaviza picos, evita *thundering herd* e enfileira requisiÃ§Ãµes concorrentes antes de tocarem o banco. |
| **NÃ­vel 2 (Banco)** | `SELECT ... FOR UPDATE` LÃ©xico | TransaÃ§Ã£o SQL | Ordena os locks de contas de forma estritamente alfabÃ©tica por UUID (`firstID < secondID`), eliminando matematicamente deadlocks cruzados (AB-BA). |

### 4. Transactional Outbox Pattern
Desacopla a liquidaÃ§Ã£o financeira da entrega de eventos para serviÃ§os externos. Os eventos sÃ£o registrados na tabela `outbox_events` na mesma transaÃ§Ã£o ACID do ledger. Um worker em segundo plano consome os registros pendentes e realiza o despacho garantindo entrega (*at-least-once*), sem risco de inconsistÃªncia por falha de escrita dupla (*dual-write*).

### 5. ReconciliaÃ§Ã£o ContÃ¡bil AutomÃ¡tica (*Nightly Audit*)
Um serviÃ§o de auditoria periÃ³dica comprova a integridade matemÃ¡tica do sistema avaliando:

$$\Delta = \text{account\_balances.current\_balance\_cents} - \sum (\text{CREDIT} - \text{DEBIT})$$

Se $\Delta \neq 0$, o sistema emite alerta crÃ­tico imediato e cataloga a divergÃªncia na tabela `audit_discrepancies`.

---

## Particionamento Mensal do Banco de Dados

A tabela `ledger_entries` Ã© particionada declarativamente por intervalo de datas (`PARTITION BY RANGE (created_at)`):

* **PartiÃ§Ãµes Mensais:** Cada mÃªs armazena registros em tabelas isoladas (ex: `ledger_entries_2026_09`).
* **Partition Pruning:** Consultas filtram apenas a partiÃ§Ã£o relevante, mantendo Ã­ndices em memÃ³ria RAM e evitando degradaÃ§Ã£o sob bilhÃµes de transaÃ§Ãµes.
* **Provisionamento AutÃ´nomo:** Um worker em background roda rotinas PL/pgSQL que criam e mantÃªm provisionadas as partiÃ§Ãµes para os prÃ³ximos 12 meses de forma automÃ¡tica.

---

## ValidaÃ§Ã£o e Benchmark de ConcorrÃªncia

A engine conta com uma suÃ­te de teste de estresse concorrente (`cmd/stress/main.go`) simulando o cenÃ¡rio clÃ¡ssico de corrida bancÃ¡ria (*Bank Run*):

* **Saldo Inicial:** R$ 1.000,00 (100.000 centavos).
* **Carga:** 50 goroutines disparadas simultaneamente no mesmo nanossegundo.
* **Valor unitÃ¡rio:** R$ 30,00 (3.000 centavos) por transaÃ§Ã£o.
* **Demanda Total:** R$ 1.500,00 (50% superior ao saldo existente).

```text
==================================================
   FINTECH CORE ENGINE - TESTE DE CARGA CONCORRENTE
==================================================
[SETUP] Conta Origem criada: ad43f92b... | Saldo: R$ 1000.00
[SETUP] Conta Destino criada: d6ec5c8c... | Saldo: R$ 0.00
--------------------------------------------------
[DISPARO] 50 requisiÃ§Ãµes paralelas de R$ 30.00...
[DEMANDA] Total Demandado: R$ 1500.00 | Saldo: R$ 1000.00
--------------------------------------------------
Tempo Total Decorrido: 231.05ms
Throughput MÃ©dio     : 216.40 req/seg
TransferÃªncias com SUCESSO: 33 (Esperado: 33)
TransferÃªncias REJEITADAS : 17 (Esperado: 17 por saldo/lock)
--------------------------------------------------
             CONFERÃŠNCIA DE BALANÃ‡OS              
--------------------------------------------------
Saldo Final Origem : R$ 10.00 (Centavos: 1000)
Saldo Final Destino: R$ 990.00 (Centavos: 99000)
Soma dos BalanÃ§os  : R$ 1000.00 (Esperado: R$ 1.000,00)
==================================================
âœ“ SUCESSO: Zero race conditions detectadas!
âœ“ ConservaÃ§Ã£o de capital e travas atÃ´micas 100% validadas.
==================================================
Como Executar
PrÃ©-requisitos
Docker Engine 24+ & Docker Compose v2+

Git

1. Inicializar o Ambiente
Clone o repositÃ³rio e inicialize os containers do PostgreSQL, Redis, API, Prometheus e Grafana:

Bash
docker compose up -d --build
2. Verificar a SaÃºde da Engine
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
4. Executar ReconciliaÃ§Ã£o ContÃ¡bil Sob Demanda
Bash
curl -s -X POST [http://127.0.0.1:8080/admin/reconciliation](http://127.0.0.1:8080/admin/reconciliation) \
Observabilidade e Monitoramento
Prometheus: MÃ©tricas disponÃ­veis em http://localhost:9090 (raspando /metrics a cada 5 segundos).

Grafana: Painel pronto em http://localhost:3000 (UsuÃ¡rio: admin / Senha: admin).

MÃ©tricas Expostas
fintech_transfers_total{status="settled|rejected", reason="..."}: Contador de liquidaÃ§Ãµes e rejeiÃ§Ãµes categorizadas.

fintech_transfer_duration_seconds: Histograma de latÃªncia transacional (percentis p50, p95 e p99).

fintech_ledger_entries_total{entry_type="CREDIT|DEBIT"}: Rastreamento em tempo real de dÃ©bitos e crÃ©ditos.

fintech_active_distributed_locks: Monitoramento de contenÃ§Ã£o de travas no Redis.

ReferÃªncia da API

Criar Conta
POST /accounts

Bash
curl -X POST [http://127.0.0.1:8080/accounts](http://127.0.0.1:8080/accounts) \
  -H "Content-Type: application/json" \
  -d '{
    "owner_name": "Raphael Diniz",
    "account_type": "CLIENT",
    "initial_balance_cents": 50000
  }'
Consultar Saldo em Tempo Real
GET /accounts/{id}/balance

Bash
curl -X GET [http://127.0.0.1:8080/accounts/](http://127.0.0.1:8080/accounts/){account_id}/balance \
Executar TransferÃªncia AtÃ´mica
POST /transfers

Bash
curl -X POST [http://127.0.0.1:8080/transfers](http://127.0.0.1:8080/transfers) \
  -H "Content-Type: application/json" \
  -d '{
    "from_account_id": "834164b3-b541-4547-8a6a-5c2132d731e0",
    "to_account_id": "f516a70a-4424-42b7-a3f2-1d54d24a0d9e",
    "amount_cents": 15000,
    "description": "LiquidaÃ§Ã£o Pix"
  }'
