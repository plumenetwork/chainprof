package chainprof

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

func BatchSendTransactionsForAccount(
	client *ethclient.Client,
	key *keystore.Key,
	password string,
	calldata []byte,
	to string,
	value *big.Int,
	times uint,
) ([]*types.Transaction, []TransactionResult, error) {

	var (
		transactions       []*types.Transaction
		results            []TransactionResult
		sendWg             sync.WaitGroup
		resultsChan        = make(chan TransactionResult, times)
		transactionsChan   = make(chan *types.Transaction, times)
		nonceMutex         sync.Mutex
		maxRetries         = 3            // Define the number of retries // Define timeout if needed elsewhere
	)

	// Initialize nonce for the account
	nonce, err := client.PendingNonceAt(context.Background(), key.Address)
	if err != nil {
		return transactions, results, err
	}

	for i := uint(0); i < times; i++ {
		sendWg.Add(1)

		go func() {
			defer sendWg.Done()

			var (
				transaction *types.Transaction
				result      TransactionResult
				sendErr     error
			)

			// Retry loop for sending transactions
			for retry := 0; retry < maxRetries; retry++ {
				// Lock nonce to ensure each goroutine uses a unique nonce
				nonceMutex.Lock()
				currentNonce := nonce
				nonce++
				nonceMutex.Unlock()

				// Create transaction options with current nonce
				opts := OptTx{
					Nonce: currentNonce,
				}

				// Attempt to send transaction
				transaction, result, sendErr = SendTransaction(client, key, password, calldata, to, value, opts)
				if sendErr == nil {
					// Success
					resultsChan <- result
					transactionsChan <- transaction
					break
				} else {
					// Log error and retry
					fmt.Fprintf(os.Stderr, "Error sending transaction (retry %d/%d): %v\n", retry+1, maxRetries, sendErr)
					time.Sleep(2 * time.Second) // Backoff between retries
				}
			}

			if sendErr != nil {
				fmt.Fprintf(os.Stderr, "Failed to send transaction after %d retries: %v\n", maxRetries, sendErr)
			}
		}()
	}

	// Wait for all goroutines to finish
	sendWg.Wait()
	close(resultsChan)
	close(transactionsChan)

	// Collect all results
	for result := range resultsChan {
		results = append(results, result)
	}
	for transaction := range transactionsChan {
		transactions = append(transactions, transaction)
	}

	return transactions, results, nil
}


// func BatchSendTransactionsForAccount(
// 	client *ethclient.Client,
// 	key *keystore.Key,
// 	password string,
// 	calldata []byte,
// 	to string,
// 	value *big.Int,
// 	times uint,
// ) ([]*types.Transaction, []TransactionResult, error) {

// 	transactions := []*types.Transaction{}
// 	results := []TransactionResult{}
// 	var sendWg sync.WaitGroup
// 	var resultWg sync.WaitGroup
// 	resultsChan := make(chan TransactionResult)
// 	transactionsChan := make(chan *types.Transaction)

// 	nonce, nonceErr := client.PendingNonceAt(context.Background(), key.Address)
// 	if nonceErr != nil {
// 		return transactions, results, nonceErr
// 	}

// 	for index := uint(0); index < times; index++ {
// 		sendWg.Add(1)
// 		go func(nonceValue uint) {
// 			defer sendWg.Done()

// 			opts := OptTx{
// 				Nonce: nonce + uint64(nonceValue),
// 			}

// 			transaction, result, transactionErr := SendTransaction(client, key, password, calldata, to, value, opts)
// 			if transactionErr != nil {
// 				fmt.Fprintln(os.Stderr, transactionErr.Error())
// 				return
// 			}

// 			resultsChan <- result
// 			transactionsChan <- transaction
// 		}(index)
// 	}

// 	resultWg.Add(1)
// 	go func() {
// 		defer resultWg.Done()
// 		for transaction := range transactionsChan {
// 			transactions = append(transactions, transaction)
// 		}
// 	}()

// 	resultWg.Add(1)
// 	go func() {
// 		defer resultWg.Done()
// 		for result := range resultsChan {
// 			results = append(results, result)
// 		}
// 	}()

// 	sendWg.Wait()
// 	close(resultsChan)
// 	close(transactionsChan)
// 	resultWg.Wait()

// 	return transactions, results, nil
// }

func BatchWaitForTransactionsToBeMined(client *ethclient.Client, transactions []*types.Transaction) ([]*types.Receipt, error) {
	var (
		waitWg     sync.WaitGroup
		results    []*types.Receipt
		resultsMux sync.Mutex // Mutex to safely access results slice
		maxRetries = 3        // Maximum retries for fetching receipt
	)

	fmt.Printf("Adding %d transactions to the queue to be mined\n", len(transactions))
	for _, transaction := range transactions {
		waitWg.Add(1)
		go func(tx *types.Transaction) {
			defer waitWg.Done()

			var receipt *types.Receipt
			var receiptErr error

			// Retry logic for fetching receipt
			for attempt := 1; attempt <= maxRetries; attempt++ {
				receipt, receiptErr = bind.WaitMined(context.Background(), client, tx)
				if receiptErr == nil {
					break
				}
				fmt.Fprintf(os.Stderr, "Error waiting for transaction to be mined (attempt %d/%d): %v\n", attempt, maxRetries, receiptErr)
				time.Sleep(time.Duration(attempt) * 2 * time.Second) // Exponential backoff
			}

			if receiptErr != nil {
				fmt.Fprintf(os.Stderr, "Failed to get receipt after %d attempts: %v\n", maxRetries, receiptErr)
				return
			}

			// Safely append to results slice
			resultsMux.Lock()
			results = append(results, receipt)
			resultsMux.Unlock()
		}(transaction)
	}

	fmt.Printf("Waiting for all %d transactions to be mined\n", len(transactions))
	waitWg.Wait()
	fmt.Printf("All %d transactions mined!\n", len(transactions))

	return results, nil
}

func UpdateTransactionResultsWithReceipts(results []TransactionResult, receipts []*types.Receipt) []TransactionResult {
	for index, receipt := range receipts {
		if index >= len(results) {
			break
		}
		results[index].GasUsed = fmt.Sprintf("%d", receipt.GasUsed)
		results[index].GasPrice = receipt.EffectiveGasPrice.String()
		results[index].BlockNumber = receipt.BlockNumber.Uint64()
	}
	return results
}



// func GetPerformanceFromTransactionResults(
// 	transactionResults []TransactionResult,
// 	rpcURL string, accounts []common.Address,
// 	calldata []byte,
// 	to common.Address,
// 	value *big.Int,
// 	transactionsPerAccount uint,
// 	startTime time.Time,
// 	endTime time.Time,
// ) (ChainPerfomance, error) {
// 	chainPerfomance := ChainPerfomance{}

// 	var initialBlockNumber, finalBlockNumber uint64
// 	var blockProductionTime time.Duration
// 	totalGasUsed, totalGasPrice := new(big.Int), new(big.Int)

// 	for _, transactionResult := range transactionResults {
// 		parsedGasUsed, parsedGasUsedSuccess := big.NewInt(0).SetString(transactionResult.GasUsed, 10)
// 		parsedGasPrice, parsedGasPriceSuccess := big.NewInt(0).SetString(transactionResult.GasPrice, 10)
// 		if !parsedGasUsedSuccess || !parsedGasPriceSuccess {
// 			continue
// 		}

// 		totalGasUsed = totalGasUsed.Add(totalGasUsed, parsedGasUsed)
// 		totalGasPrice = totalGasPrice.Add(totalGasPrice, parsedGasPrice)

// 		if initialBlockNumber == 0 {
// 			initialBlockNumber = transactionResult.BlockNumber
// 		} else if transactionResult.BlockNumber < initialBlockNumber {
// 			initialBlockNumber = transactionResult.BlockNumber
// 		}

// 		if transactionResult.BlockNumber > finalBlockNumber {
// 			finalBlockNumber = transactionResult.BlockNumber
// 		}
// 	}

// 	avgGasUsed := new(big.Int).Div(totalGasUsed, big.NewInt(int64(len(transactionResults))))
// 	avgGasPrice := new(big.Int).Div(totalGasPrice, big.NewInt(int64(len(transactionResults))))

// 	initialBlockTimestamp, initialBlockTimestampErr := ArbitrumGetBlockTimestampByBlockNumber(rpcURL, big.NewInt(int64(initialBlockNumber)))
// 	if initialBlockTimestampErr != nil {
// 		return chainPerfomance, initialBlockTimestampErr
// 	}

// 	latestBlockTimestamp, latestBlockTimestampErr := ArbitrumGetBlockTimestampByBlockNumber(rpcURL, big.NewInt(int64(finalBlockNumber)))
// 	if latestBlockTimestampErr != nil {
// 		return chainPerfomance, latestBlockTimestampErr
// 	}

// 	blockProductionTime = latestBlockTimestamp.Sub(initialBlockTimestamp)
// 	executionTime := endTime.Sub(startTime)

// 	chainPerfomance = ChainPerfomance{
// 		RPC:                    rpcURL,
// 		Calldata:               hex.EncodeToString(calldata),
// 		To:                     to.String(),
// 		Value:                  value.String(),
// 		ExecutionTime:          int(executionTime.Seconds()),
// 		BlockProductionTime:    int(blockProductionTime.Seconds()),
// 		InitialBlockNumber:     initialBlockNumber,
// 		FinalBlockNumber:       finalBlockNumber,
// 		InitialBlockTimestamp:  initialBlockTimestamp.Format("2006-01-02 15:04:05"),
// 		FinalBlockTimestamp:    latestBlockTimestamp.Format("2006-01-02 15:04:05"),
// 		TransactionsPerAccount: transactionsPerAccount,
// 		AvgGasUsed:             avgGasUsed.String(),
// 		AvgGasPrice:            avgGasPrice.String(),
// 		TotalGasUsed:           totalGasUsed.String(),
// 		TotalGasPrice:          totalGasPrice.String(),
// 		TotalTransactionsSent:  len(transactionResults),
// 		Accounts:               accounts,
// 		Transactions:           transactionResults,
// 	}

// 	return chainPerfomance, nil
// }
func GetPerformanceFromTransactionResults(
	transactionResults []TransactionResult,
	rpcURL string,
	accounts []common.Address,
	calldata []byte,
	to common.Address,
	value *big.Int,
	transactionsPerAccount uint,
	startTime time.Time,
	endTime time.Time,
) (ChainPerfomance, error) {
	chainPerfomance := ChainPerfomance{}

	// Check if transactionResults has any entries
	if len(transactionResults) == 0 {
		fmt.Println("No transactions were processed.")
		return chainPerfomance, fmt.Errorf("no transactions to process")
	}

	var initialBlockNumber, finalBlockNumber uint64
	var blockProductionTime time.Duration
	totalGasUsed, totalGasPrice := new(big.Int), new(big.Int)

	for _, transactionResult := range transactionResults {
		parsedGasUsed, parsedGasUsedSuccess := big.NewInt(0).SetString(transactionResult.GasUsed, 10)
		parsedGasPrice, parsedGasPriceSuccess := big.NewInt(0).SetString(transactionResult.GasPrice, 10)
		if !parsedGasUsedSuccess || !parsedGasPriceSuccess {
			continue
		}

		totalGasUsed = totalGasUsed.Add(totalGasUsed, parsedGasUsed)
		totalGasPrice = totalGasPrice.Add(totalGasPrice, parsedGasPrice)

		if initialBlockNumber == 0 || transactionResult.BlockNumber < initialBlockNumber {
			initialBlockNumber = transactionResult.BlockNumber
		}

		if transactionResult.BlockNumber > finalBlockNumber {
			finalBlockNumber = transactionResult.BlockNumber
		}
	}

	avgGasUsed := new(big.Int).Div(totalGasUsed, big.NewInt(int64(len(transactionResults))))
	avgGasPrice := new(big.Int).Div(totalGasPrice, big.NewInt(int64(len(transactionResults))))

	initialBlockTimestamp, initialBlockTimestampErr := ArbitrumGetBlockTimestampByBlockNumber(rpcURL, big.NewInt(int64(initialBlockNumber)))
	if initialBlockTimestampErr != nil {
		return chainPerfomance, initialBlockTimestampErr
	}

	latestBlockTimestamp, latestBlockTimestampErr := ArbitrumGetBlockTimestampByBlockNumber(rpcURL, big.NewInt(int64(finalBlockNumber)))
	if latestBlockTimestampErr != nil {
		return chainPerfomance, latestBlockTimestampErr
	}

	blockProductionTime = latestBlockTimestamp.Sub(initialBlockTimestamp)
	executionTime := endTime.Sub(startTime)

	chainPerfomance = ChainPerfomance{
		RPC:                    rpcURL,
		Calldata:               hex.EncodeToString(calldata),
		To:                     to.String(),
		Value:                  value.String(),
		ExecutionTime:          int(executionTime.Seconds()),
		BlockProductionTime:    int(blockProductionTime.Seconds()),
		InitialBlockNumber:     initialBlockNumber,
		FinalBlockNumber:       finalBlockNumber,
		InitialBlockTimestamp:  initialBlockTimestamp.Format("2006-01-02 15:04:05"),
		FinalBlockTimestamp:    latestBlockTimestamp.Format("2006-01-02 15:04:05"),
		TransactionsPerAccount: transactionsPerAccount,
		AvgGasUsed:             avgGasUsed.String(),
		AvgGasPrice:            avgGasPrice.String(),
		TotalGasUsed:           totalGasUsed.String(),
		TotalGasPrice:          totalGasPrice.String(),
		TotalTransactionsSent:  len(transactionResults),
		Accounts:               accounts,
		Transactions:           transactionResults,
	}

	return chainPerfomance, nil
}
