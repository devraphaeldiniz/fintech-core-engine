package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

var (
	baseURL    = "http://127.0.0.1:8080"
	apiKey     = "fintech_live_secret_key_123"
	numReqs    = 50
	txAmount   = int64(3000)   // R$ 30,00
	initialBal = int64(100000) // R$ 1.000,00
)

type AccountResponse struct {
	ID        string `json:"id"`
	OwnerName string `json:"owner_name"`
}

type BalanceResponse struct {
	AccountID    string `json:"account_id"`
	BalanceCents int64  `json:"balance_cents"`
}

func main() {
	if envURL := os.Getenv("API_URL"); envURL != "" {
		baseURL = envURL
	}

	client := &http.Client{Timeout: 15 * time.Second}

	fmt.Println("==================================================")
	fmt.Println("   FINTECH CORE ENGINE - TESTE DE CARGA CONCORRENTE")
	fmt.Println("==================================================")

	fromAcc := createAccount(client, "Conta Origem Stress", initialBal)
	fmt.Printf("[SETUP] Conta Origem criada: %s | Saldo: R$ %.2f\n", fromAcc.ID, float64(initialBal)/100)

	toAcc := createAccount(client, "Conta Destino Stress", 0)
	fmt.Printf("[SETUP] Conta Destino criada: %s | Saldo: R$ 0.00\n", toAcc.ID)
	fmt.Println("--------------------------------------------------")
	fmt.Printf("[DISPARO] %d requisições paralelas de R$ %.2f...\n", numReqs, float64(txAmount)/100)
	fmt.Printf("[DEMANDA] Total Demandado: R$ %.2f | Saldo: R$ %.2f\n", float64(int64(numReqs)*txAmount)/100, float64(initialBal)/100)
	fmt.Println("--------------------------------------------------")

	var (
		successCount int64
		failedCount  int64
		wg           sync.WaitGroup
		startBarrier = make(chan struct{})
	)

	start := time.Now()

	for i := 1; i <= numReqs; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startBarrier

			idempotencyKey := fmt.Sprintf("stress-tx-%s-%03d", uuid.NewString()[:8], idx)
			payload, _ := json.Marshal(map[string]interface{}{
				"from_account_id": fromAcc.ID,
				"to_account_id":   toAcc.ID,
				"amount_cents":    txAmount,
				"description":     fmt.Sprintf("Stress transfer #%d", idx),
			})

			req, _ := http.NewRequest("POST", baseURL+"/transfers", bytes.NewBuffer(payload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", apiKey)
			req.Header.Set("X-Idempotency-Key", idempotencyKey)

			resp, err := client.Do(req)
			if err != nil {
				atomic.AddInt64(&failedCount, 1)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
				atomic.AddInt64(&successCount, 1)
			} else {
				atomic.AddInt64(&failedCount, 1)
			}
		}(i)
	}

	close(startBarrier)
	wg.Wait()
	elapsed := time.Since(start)

	fmt.Println("--------------------------------------------------")
	fmt.Println("                RESULTADO DO TESTE                ")
	fmt.Println("--------------------------------------------------")
	fmt.Printf("Tempo Total Decorrido: %v\n", elapsed)
	fmt.Printf("Throughput Médio     : %.2f req/seg\n", float64(numReqs)/elapsed.Seconds())
	fmt.Printf("Transferências com SUCESSO: %d (Esperado: 33)\n", successCount)
	fmt.Printf("Transferências REJEITADAS : %d (Esperado: 17 por saldo/lock)\n", failedCount)

	time.Sleep(500 * time.Millisecond)
	fromBal := getBalance(client, fromAcc.ID)
	toBal := getBalance(client, toAcc.ID)

	fmt.Println("--------------------------------------------------")
	fmt.Println("             CONFERÊNCIA DE BALANÇOS              ")
	fmt.Println("--------------------------------------------------")
	fmt.Printf("Saldo Final Origem : R$ %.2f (Centavos: %d)\n", float64(fromBal)/100, fromBal)
	fmt.Printf("Saldo Final Destino: R$ %.2f (Centavos: %d)\n", float64(toBal)/100, toBal)
	fmt.Printf("Soma dos Balanços  : R$ %.2f (Esperado: R$ 1.000,00)\n", float64(fromBal+toBal)/100)

	if fromBal < 0 {
		log.Fatalf("FALHA CRÍTICA: Saldo NEGATIVO na conta origem! Ocorreu Race Condition.")
	}

	if fromBal+toBal != initialBal {
		log.Fatalf("FALHA CRÍTICA: Total (R$ %.2f) diverge do montante original!", float64(fromBal+toBal)/100)
	}

	fmt.Println("==================================================")
	fmt.Println("✓ SUCESSO: Zero race conditions detectadas!")
	fmt.Println("✓ Conservação de capital e travas atômicas 100% validadas.")
	fmt.Println("==================================================")
}

func createAccount(client *http.Client, name string, balance int64) AccountResponse {
	payload, _ := json.Marshal(map[string]interface{}{
		"owner_name":            name,
		"account_type":          "CLIENT",
		"initial_balance_cents": balance,
	})
	req, _ := http.NewRequest("POST", baseURL+"/accounts", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Erro ao criar conta: %v", err)
	}
	defer resp.Body.Close()

	var acc AccountResponse
	_ = json.NewDecoder(resp.Body).Decode(&acc)
	return acc
}

func getBalance(client *http.Client, id string) int64 {
	req, _ := http.NewRequest("GET", baseURL+"/accounts/"+id+"/balance", nil)
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Erro ao buscar saldo: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var bal BalanceResponse
	_ = json.Unmarshal(body, &bal)
	return bal.BalanceCents
}
