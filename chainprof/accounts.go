package chainprof

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"

	"github.com/G7DAO/chainprof/bindings/ERC20"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"strings"
	"github.com/ethereum/go-ethereum/accounts/abi"
)

// PrepareCalldataForTestCompute prepares the calldata for the testCompute function with iterations as an argument.
func PrepareCalldataForTestCompute(iterations uint64) ([]byte, error) {
	// Define the ABI JSON for the function
	contractABI, err := abi.JSON(strings.NewReader(`[{"name": "testCompute", "type": "function", "inputs": [{"name": "iterations", "type": "uint256"}], "outputs": []}]`))
	if err != nil {
		return nil, err
	}

	// Pack the arguments for the `testCompute(uint256)` function
	iterations_ := 10000
	calldata, err := contractABI.Pack("testCompute", big.NewInt(int64(iterations_)))
	if err != nil {
		return nil, err
	}

	return calldata, nil
}

func CreateAccounts(accountsDir string, numAccounts int, password string) error {
	fmt.Println("WARNING: This is a *very* insecure method to generate accounts. It is using insecure ScryptN and ScryptP parameters! Do not use this for ANYTHING important please.")
	s := keystore.NewKeyStore(accountsDir, 2, 8)

	for i := 0; i < numAccounts; i++ {
		_, err := s.NewAccount(password)
		if err != nil {
			return err
		}
	}

	return nil
}

func FundAccounts(rpcURL string, accountsDir string, keyFile string, password string, value *big.Int) ([]TransactionResult, error) {
	results := []TransactionResult{}

	recipients, recipientErr := ReadAccounts(accountsDir)
	if recipientErr != nil {
		return results, recipientErr
	}

	client, clientErr := ethclient.Dial(rpcURL)
	if clientErr != nil {
		return results, clientErr
	}

	key, keyErr := ERC20.KeyFromFile(keyFile, password)
	if keyErr != nil {
		return results, keyErr
	}

	transactions, results, resultsErr := BatchFundAccounts(client, key, password, []byte{}, recipients, value)
	if resultsErr != nil {
		return results, resultsErr
	}

	receipts, receiptsErr := BatchWaitForTransactionsToBeMined(client, transactions)
	if receiptsErr != nil {
		return results, receiptsErr
	}

	results = UpdateTransactionResultsWithReceipts(results, receipts)

	return results, nil
}

func FundAccountsERC20(rpcURL string, accountsDir string, keyFile string, password string, tokenAddress string, value *big.Int) ([]TransactionResult, error) {
	results := []TransactionResult{}

	recipients, recipientErr := ReadAccounts(accountsDir)
	if recipientErr != nil {
		return results, recipientErr
	}

	client, clientErr := ethclient.Dial(rpcURL)
	if clientErr != nil {
		return results, clientErr
	}

	key, keyErr := ERC20.KeyFromFile(keyFile, password)
	if keyErr != nil {
		return results, keyErr
	}

	transactions, results, resultsErr := BatchFundAccountsERC20(client, key, password, tokenAddress, recipients, value)
	if resultsErr != nil {
		return results, resultsErr
	}

	receipts, receiptsErr := BatchWaitForTransactionsToBeMined(client, transactions)
	if receiptsErr != nil {
		return results, receiptsErr
	}

	results = UpdateTransactionResultsWithReceipts(results, receipts)

	return results, nil
}

func DrainAccounts(rpcURL string, accountsDir string, recipientAddress string, password string) ([]TransactionResult, error) {
	results := []TransactionResult{}

	client, clientErr := ethclient.Dial(rpcURL)
	if clientErr != nil {
		return results, clientErr
	}

	transactions, results, resultsErr := BatchDrainAccounts(client, accountsDir, recipientAddress, password)
	if resultsErr != nil {
		return results, resultsErr
	}

	receipts, receiptsErr := BatchWaitForTransactionsToBeMined(client, transactions)
	if receiptsErr != nil {
		return results, receiptsErr
	}

	results = UpdateTransactionResultsWithReceipts(results, receipts)

	return results, nil
}

func DrainAccountsERC20(rpcURL string, accountsDir string, recipientAddress string, password string, tokenAddress string) ([]TransactionResult, error) {
	results := []TransactionResult{}

	client, clientErr := ethclient.Dial(rpcURL)
	if clientErr != nil {
		return results, clientErr
	}

	transactions, results, resultsErr := BatchDrainAccountsERC20(client, accountsDir, recipientAddress, password, tokenAddress)
	if resultsErr != nil {
		return results, resultsErr
	}

	receipts, receiptsErr := BatchWaitForTransactionsToBeMined(client, transactions)
	if receiptsErr != nil {
		return results, receiptsErr
	}

	results = UpdateTransactionResultsWithReceipts(results, receipts)

	return results, nil
}

func EvaluateAccount(rpcURL string, accountsDir string, password string, calldata []byte, to string, value *big.Int, transactionsPerAccount uint, iterations uint64) ([]TransactionResult, []common.Address, error) {
	results := []TransactionResult{}
	transactions := []*types.Transaction{}
	accounts := []common.Address{}

	// Read account keys
	keyFiles, keyFileErr := os.ReadDir(accountsDir)
	if keyFileErr != nil {
		return results, accounts, keyFileErr
	}

	// Connect to client
	client, clientErr := ethclient.Dial(rpcURL)
	if clientErr != nil {
		return results, accounts, clientErr
	}

	fmt.Printf("Sending %d transactions for each of the %d accounts\n", transactionsPerAccount, len(keyFiles))

	var wg sync.WaitGroup
	var mu sync.Mutex // For safely appending to results and transactions slices

	for i, keyFile := range keyFiles {
		key, keyErr := ERC20.KeyFromFile(filepath.Join(accountsDir, keyFile.Name()), password)
		if keyErr != nil {
			fmt.Fprintln(os.Stderr, "Error reading key:", keyErr)
			continue
		}
		accounts = append(accounts, key.Address)

		wg.Add(1)
		go func(i int, key *keystore.Key) {
			defer wg.Done()

			fmt.Printf("%.2f%% - Sending %d transactions for account %s\n", float64(i+1)/float64(len(keyFiles))*100, transactionsPerAccount, key.Address.Hex())

			// Prepare unique calldata per account if necessary
			localCalldata, err := PrepareCalldataForTestCompute(iterations)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Failed to prepare calldata:", err.Error())
				return
			}

			// Send transactions for each account in parallel
			accountTransactions, accountResults, sendErr := BatchSendTransactionsForAccount(client, key, password, localCalldata, to, value, transactionsPerAccount)
			if sendErr != nil {
				fmt.Fprintln(os.Stderr, "Transaction sending error:", sendErr)
				return
			}

			// Lock before appending to shared results and transactions
			mu.Lock()
			transactions = append(transactions, accountTransactions...)
			results = append(results, accountResults...)
			mu.Unlock()
		}(i, key)
	}

	// Wait for all goroutines to finish
	wg.Wait()

	// Wait for transactions to be mined
	receipts, receiptsErr := BatchWaitForTransactionsToBeMined(client, transactions)
	if receiptsErr != nil {
		return results, accounts, receiptsErr
	}

	// Update transaction results with receipts
	results = UpdateTransactionResultsWithReceipts(results, receipts)

	return results, accounts, nil
}
