package db

import (
	"context"
	"database/sql"
	"fmt"
)

// a generic interfce for store
type Store interface {
	Querier
	TransferTx(ctx context.Context, arg TransferTxParams) (TransferTxResult, error)
}

// SQLStore provides all functions to execute SQL queries and transactions - a real db (postgres in app)
type SQLStore struct {
	*Queries // extend struct functionality in golang - inheritance equivalent
	db       *sql.DB
}

func NewStore(db *sql.DB) Store {
	return &SQLStore{db: db, Queries: New(db)}
}

// execTx executes a function within a database transaction
func (store *SQLStore) execTx(ctx context.Context, fn func(*Queries) error) error {
	tx, err := store.db.BeginTx(ctx, nil) // &sql.TxOptions{} - todo later
	if err != nil {
		return err
	}
	q := New(tx) // New can work with either *sql.DB or *sql.Tx - DBTX interface
	err = fn(q)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("tx error: %v, rb error: %v", err, rbErr)
		}
		return err
	}
	return tx.Commit()
}

// TransferTxParams contains the input parameters of the transfer transaction
type TransferTxParams struct {
	FromAccountId int64 `json:"from_account_id"`
	ToAccountId   int64 `json:"to_account_id"`
	Amount        int64 `json:"amount"`
}

// TransferTxResult contains the result of the transfer transaction
type TransferTxResult struct {
	Transfer    Transfer `json:"transfer"`
	FromAccount Account  `json:"from_account"`
	ToAccount   Account  `json:"to_account"`
	FromEntry   Entry    `json:"from_entry"`
	ToEntry     Entry    `json:"to_entry"`
}

var txKey = struct{}{}

// TransferTx performs a money transfer from one account to other
// It creates a transfer record, add account entries, and update accounts' balance within a single db tx
func (store *SQLStore) TransferTx(ctx context.Context, arg TransferTxParams) (TransferTxResult, error) {
	var result TransferTxResult

	err := store.execTx(ctx, func(q *Queries) error {
		var err error

		// get tx name from ctx
		txName := ctx.Value(txKey)

		// transfer
		fmt.Println(txName, "create Transfer")
		result.Transfer, err = q.CreateTransfer(ctx, CreateTransferParams{
			FromAccountID: arg.FromAccountId,
			ToAccountID:   arg.ToAccountId,
			Amount:        arg.Amount,
		})
		if err != nil {
			return err
		}

		// from entry
		fmt.Println(txName, "create FromEntry")
		result.FromEntry, err = q.CreateEntry(ctx, CreateEntryParams{
			AccountID: arg.FromAccountId,
			Amount:    -arg.Amount,
		})
		if err != nil {
			return err
		}

		// to entry
		fmt.Println(txName, "create ToEntry")
		result.ToEntry, err = q.CreateEntry(ctx, CreateEntryParams{
			AccountID: arg.ToAccountId,
			Amount:    arg.Amount,
		})
		if err != nil {
			return err
		}

		// get account ->  update from accounts' balance
		// fmt.Println(txName, "get accountFrom")
		// accountFrom, err := q.GetAccountForUpdate(ctx, arg.FromAccountId)
		// if err != nil {
		// 	return err
		// }
		// fmt.Println(txName, "update accountFrom")
		// result.FromAccount, err = q.UpdateAccount(ctx, UpdateAccountParams{
		// 	ID:      arg.FromAccountId,
		// 	Balance: accountFrom.Balance - arg.Amount,
		// })
		// if err != nil {
		// 	return err
		// }

		// get account ->  update to accounts' balance
		// fmt.Println(txName, "get accountTo")
		// accountTo, err := q.GetAccountForUpdate(ctx, arg.ToAccountId)
		// if err != nil {
		// 	return err
		// }
		// fmt.Println(txName, "update accountTo")
		// result.ToAccount, err = q.UpdateAccount(ctx, UpdateAccountParams{
		// 	ID:      arg.ToAccountId,
		// 	Balance: accountTo.Balance + arg.Amount,
		// })
		// if err != nil {
		// 	return err
		// }

		// update accounts' balance in a consistent order (lower account id first) - avoid deadlock
		if arg.FromAccountId < arg.ToAccountId {
			// update fromAccount first as it is lower account id here
			result.FromAccount, result.ToAccount, err = addAmountInOrder(ctx, q, arg.FromAccountId, -arg.Amount, arg.ToAccountId, arg.Amount)
			if err != nil {
				return err
			}
		} else {
			// update toAccount first as it is lower account id here
			result.ToAccount, result.FromAccount, err = addAmountInOrder(ctx, q, arg.ToAccountId, arg.Amount, arg.FromAccountId, -arg.Amount)
			if err != nil {
				return err
			}
		}
		return nil
	})

	return result, err
}

// addAmountInOrder is a helper func to make transfer happen in order
func addAmountInOrder(ctx context.Context, q *Queries, AccountID1 int64, amount1 int64, AccountID2 int64, amount2 int64) (account1 Account, account2 Account, err error) {
	// update account1 balance with 1 single query
	account1, err = q.UpdateAccountBalance(ctx, UpdateAccountBalanceParams{
		ID:     AccountID1,
		Amount: amount1,
	})
	if err != nil {
		return
	}
	// update account2 balance with 1 single query
	account2, err = q.UpdateAccountBalance(ctx, UpdateAccountBalanceParams{
		ID:     AccountID2,
		Amount: amount2,
	})
	return
}
