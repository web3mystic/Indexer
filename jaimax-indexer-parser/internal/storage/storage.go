package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
    "strings"
	"github.com/cosmos-indexer/pkg/types"
	_ "github.com/lib/pq"
)

// Storage defines the persistence contract for the indexer.
//
// It abstracts database implementation details so that the
// indexer core logic remains storage-agnostic (Postgres, etc.).
//
// All methods are context-aware to support cancellation,
// timeouts, and graceful shutdown.
type Storage interface {
	// Block operations
	StoreBlock(ctx context.Context, block *types.ParsedBlock) error
	StoreTx(ctx context.Context, tx *types.ParsedTx) error
	GetLastIndexedHeight(ctx context.Context) (int64, error)
	UpdateIndexerHeight(ctx context.Context, height int64, blockHash string) error

	// Validator operations
	StoreValidator(ctx context.Context, validator *types.ParsedValidator) error
	StoreValidators(ctx context.Context, validators []*types.ParsedValidator) error
	GetValidator(ctx context.Context, operatorAddr string) (*types.ParsedValidator, error)
	GetAllValidators(ctx context.Context) ([]*types.ParsedValidator, error)
	GetValidatorSyncState(ctx context.Context) (*types.ValidatorSyncState, error)
	UpdateValidatorSyncState(ctx context.Context, height int64) error

	// Delegation operations
	StoreDelegation(ctx context.Context, delegation *types.ParsedDelegation) error
	StoreDelegations(ctx context.Context, delegations []*types.ParsedDelegation) error
	GetDelegationsByDelegator(ctx context.Context, delegator string) ([]*types.ParsedDelegation, error)
	GetDelegationsByValidator(ctx context.Context, validator string) ([]*types.ParsedDelegation, error)

	// Balance operations
	StoreBalance(ctx context.Context, balance *types.ParsedBalance) error
	StoreBalances(ctx context.Context, balances []*types.ParsedBalance) error
	GetBalance(ctx context.Context, address, denom string) (*types.ParsedBalance, error)
	GetAllBalances(ctx context.Context, address string) ([]*types.ParsedBalance, error)

	// Proposal operations
	StoreProposal(ctx context.Context, proposal *types.ParsedProposal) error
	StoreProposals(ctx context.Context, proposals []*types.ParsedProposal) error
	GetProposal(ctx context.Context, proposalID uint64) (*types.ParsedProposal, error)
	GetActiveProposals(ctx context.Context) ([]*types.ParsedProposal, error)
	GetProposalSyncState(ctx context.Context) (*types.ProposalSyncState, error)
	UpdateProposalSyncState(ctx context.Context, height int64) error

	// Vote operations
	StoreVote(ctx context.Context, vote *types.ParsedVote) error
	StoreVotes(ctx context.Context, votes []*types.ParsedVote) error
	GetVotesByProposal(ctx context.Context, proposalID uint64) ([]*types.ParsedVote, error)
	GetVotesByVoter(ctx context.Context, voter string) ([]*types.ParsedVote, error)

	// WASM OPERATIONS
	StoreWasmCode(ctx context.Context, c *types.WasmCode) error
	UpsertWasmCode(ctx context.Context, c *types.WasmCode) error
	GetWasmCode(ctx context.Context, codeID int64) (*types.WasmCode, error)

	StoreWasmExecution(ctx context.Context, exec *types.WasmExecution) error
	StoreWasmInstantiation(ctx context.Context, inst *types.WasmInstantiation) error
	StoreWasmMigration(ctx context.Context, mig *types.WasmMigration) error

	StoreWasmEvent(ctx context.Context, event *types.WasmEvent) error
	StoreWasmEvents(ctx context.Context, events []*types.WasmEvent) error

	StoreCW20Transfer(ctx context.Context, t *types.CW20Transfer) error
	StoreCW20Transfers(ctx context.Context, ts []*types.CW20Transfer) error

	StoreBankTransfer(ctx context.Context, t *types.BankTransfer) error
	StoreBankTransfers(ctx context.Context, ts []*types.BankTransfer) error

	UpsertWasmContract(ctx context.Context, c *types.WasmContract) error
	GetWasmContract(ctx context.Context, contractAddress string) (*types.WasmContract, error)

	Close() error
}

// PostgresStorage implements the Storage interface using PostgreSQL.
// It manages connection pooling and transactional writes.
type PostgresStorage struct {
	db *sql.DB
}

// NewPostgresStorage creates a new PostgreSQL storage instance
// NewPostgresStorage initializes a PostgreSQL connection pool
// and verifies connectivity with Ping().
// Connection pool limits are tuned for moderate indexer workloads.
func NewPostgresStorage(connString string) (*PostgresStorage, error) {
	db, err := sql.Open("postgres", connString)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	return &PostgresStorage{db: db}, nil
}

// ============================================================================
// BLOCK OPERATIONS (existing)
// ============================================================================
// StoreBlock persists a full parsed block including:
//   - block metadata
//   - all transactions
//   - indexer state update
//
// Everything is wrapped in a single DB transaction to guarantee
// atomicity — either the entire block is stored, or nothing is.

func (s *PostgresStorage) StoreBlock(ctx context.Context, block *types.ParsedBlock) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Rollback is safe even after Commit; it will no-op if already committed.
	defer tx.Rollback()

	// Upsert ensures idempotency in case of re-indexing or restarts.
	blockQuery := `
		INSERT INTO blocks (height, hash, time, proposer_address, tx_count)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (height) DO UPDATE SET
			hash = EXCLUDED.hash,
			time = EXCLUDED.time,
			proposer_address = EXCLUDED.proposer_address,
			tx_count = EXCLUDED.tx_count
	`
	_, err = tx.ExecContext(ctx, blockQuery,
		block.Height, block.Hash, block.Time, block.ProposerAddr, block.TxCount,
	)
	if err != nil {
		return fmt.Errorf("failed to insert block %d: %w", block.Height, err)
	}

	for _, parsedTx := range block.Txs {
		if err := s.storeTxInTransaction(ctx, tx, &parsedTx); err != nil {
			return fmt.Errorf("failed to store tx %s: %w", parsedTx.Hash, err)
		}
	}

	stateQuery := `
		INSERT INTO indexer_state (id, last_height, last_block_hash, updated_at)
		VALUES (1, $1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET
			last_height = EXCLUDED.last_height,
			last_block_hash = EXCLUDED.last_block_hash,
			updated_at = EXCLUDED.updated_at
	`
	_, err = tx.ExecContext(ctx, stateQuery, block.Height, block.Hash, time.Now())
	if err != nil {
		return fmt.Errorf("failed to update indexer state: %w", err)
	}

	return tx.Commit()
}

func (s *PostgresStorage) StoreTx(ctx context.Context, tx *types.ParsedTx) error {
	dbTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer dbTx.Rollback()

	if err := s.storeTxInTransaction(ctx, dbTx, tx); err != nil {
		return err
	}

	return dbTx.Commit()
}

// storeTxInTransaction inserts a transaction and its related:
//   - messages
//   - events
//   - address mappings
//
// Must be called inside an existing DB transaction.

func (s *PostgresStorage) storeTxInTransaction(ctx context.Context, dbTx *sql.Tx, tx *types.ParsedTx) error {
	// Store message types and addresses as JSON arrays for flexible querying.
	msgTypesJSON, _ := json.Marshal(tx.MsgTypes)
	addressesJSON, _ := json.Marshal(tx.Addresses)

	txQuery := `
		INSERT INTO transactions (
			hash, height, tx_index, gas_used, gas_wanted, 
			fee, success, code, log, memo, 
			msg_types, addresses, timestamp
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (hash) DO UPDATE SET
			gas_used = EXCLUDED.gas_used,
			success = EXCLUDED.success,
			code = EXCLUDED.code,
			log = EXCLUDED.log
	`
	_, err := dbTx.ExecContext(ctx, txQuery,
		tx.Hash, tx.Height, tx.Index, tx.GasUsed, tx.GasWanted,
		tx.Fee, tx.Success, tx.Code, tx.Log, tx.Memo,
		msgTypesJSON, addressesJSON, tx.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("failed to insert transaction: %w", err)
	}

	for i, msg := range tx.Messages {
		rawDataJSON, _ := json.Marshal(msg.RawData)

		msgQuery := `
			INSERT INTO messages (
				tx_hash, msg_index, msg_type, sender, receiver,
				amount, denom, raw_data
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (tx_hash, msg_index) DO NOTHING
		`
		_, err = dbTx.ExecContext(ctx, msgQuery,
			tx.Hash, i, msg.Type, msg.Sender, msg.Receiver,
			msg.Amount, msg.Denom, rawDataJSON,
		)
		if err != nil {
			return fmt.Errorf("failed to insert message: %w", err)
		}
	}
// ADD THIS after the messages for loop in storeTxInTransaction:
if tx.Success {
    for _, msg := range tx.Messages {
        if strings.Contains(msg.Type, "MsgMultiSend") {
            // Credit all outputs
            if outputs, ok := msg.RawData["outputs"].([]map[string]interface{}); ok {
                for _, output := range outputs {
                    addr, _ := output["address"].(string)
                    coins, _ := output["coins"].([]map[string]interface{})
                    for _, coin := range coins {
                        denom, _ := coin["denom"].(string)
                        amount, _ := coin["amount"].(string)
                        if addr != "" && denom != "" && amount != "" && amount != "0" {
                            if err := s.upsertBalanceDelta(ctx, dbTx, addr, denom, amount, tx.Height, true); err != nil {
                                return fmt.Errorf("failed to update balance for multisend output %s: %w", addr, err)
                            }
                        }
                    }
                }
            }
            // Debit all inputs
            if inputs, ok := msg.RawData["inputs"].([]map[string]interface{}); ok {
                for _, input := range inputs {
                    addr, _ := input["address"].(string)
                    coins, _ := input["coins"].([]map[string]interface{})
                    for _, coin := range coins {
                        denom, _ := coin["denom"].(string)
                        amount, _ := coin["amount"].(string)
                        if addr != "" && denom != "" && amount != "" && amount != "0" {
                            if err := s.upsertBalanceDelta(ctx, dbTx, addr, denom, amount, tx.Height, false); err != nil {
                                return fmt.Errorf("failed to update balance for multisend input %s: %w", addr, err)
                            }
                        }
                    }
                }
            }
        } else if strings.Contains(msg.Type, "MsgSend") {
            // Also handle regular MsgSend balance updates here for consistency
            if msg.Sender != "" && msg.Denom != "" && msg.Amount != "" && msg.Amount != "0" {
                if err := s.upsertBalanceDelta(ctx, dbTx, msg.Sender, msg.Denom, msg.Amount, tx.Height, false); err != nil {
                    return fmt.Errorf("failed to debit sender balance: %w", err)
                }
                if msg.Receiver != "" {
                    if err := s.upsertBalanceDelta(ctx, dbTx, msg.Receiver, msg.Denom, msg.Amount, tx.Height, true); err != nil {
                        return fmt.Errorf("failed to credit receiver balance: %w", err)
                    }
                }
            }
        }
    }
}
	for i, event := range tx.Events {
		attrsJSON, _ := json.Marshal(event.Attributes)

		eventQuery := `
			INSERT INTO events (
				tx_hash, event_index, event_type, attributes
			)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (tx_hash, event_index) DO NOTHING
		`
		_, err = dbTx.ExecContext(ctx, eventQuery, tx.Hash, i, event.Type, attrsJSON)
		if err != nil {
			return fmt.Errorf("failed to insert event: %w", err)
		}
	}

	// Maintain address-to-transaction mapping for fast address history queries.
	for _, addr := range tx.Addresses {
		addrQuery := `
			INSERT INTO address_transactions (address, tx_hash, height)
			VALUES ($1, $2, $3)
			ON CONFLICT (address, tx_hash) DO NOTHING
		`
		_, err = dbTx.ExecContext(ctx, addrQuery, addr, tx.Hash, tx.Height)
		if err != nil {
			return fmt.Errorf("failed to insert address mapping: %w", err)
		}
	}

	return nil
}

// GetLastIndexedHeight returns the last successfully indexed block height.
// Returns 0 if the indexer has never run.
func (s *PostgresStorage) GetLastIndexedHeight(ctx context.Context) (int64, error) {
	var height sql.NullInt64
	query := `SELECT last_height FROM indexer_state WHERE id = 1`
	err := s.db.QueryRowContext(ctx, query).Scan(&height)

	if err == sql.ErrNoRows || !height.Valid {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get last indexed height: %w", err)
	}

	return height.Int64, nil
}

func (s *PostgresStorage) UpdateIndexerHeight(ctx context.Context, height int64, blockHash string) error {
	query := `
		INSERT INTO indexer_state (id, last_height, last_block_hash, updated_at)
		VALUES (1, $1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET
			last_height = EXCLUDED.last_height,
			last_block_hash = EXCLUDED.last_block_hash,
			updated_at = EXCLUDED.updated_at
	`
	_, err := s.db.ExecContext(ctx, query, height, blockHash, time.Now())
	return err
}

// ============================================================================
// VALIDATOR OPERATIONS (NEW)
// ============================================================================
// Validators are periodically synced independently from block indexing.
// Upserts are used to keep validator state current.
//
// StoreValidator inserts or updates validator state.
// Voting power and commission fields are refreshed on every sync.

func (s *PostgresStorage) StoreValidator(ctx context.Context, validator *types.ParsedValidator) error {
	query := `
		INSERT INTO validators (
			operator_address, consensus_address, consensus_pubkey, jailed, status,
			tokens, delegator_shares, moniker, identity, website, security_contact,
			details, unbonding_height, unbonding_time, commission_rate,
			commission_max_rate, commission_max_change_rate, min_self_delegation,
			voting_power, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		ON CONFLICT (operator_address) DO UPDATE SET
			consensus_address = EXCLUDED.consensus_address,
			consensus_pubkey = EXCLUDED.consensus_pubkey,
			jailed = EXCLUDED.jailed,
			status = EXCLUDED.status,
			tokens = EXCLUDED.tokens,
			delegator_shares = EXCLUDED.delegator_shares,
			moniker = EXCLUDED.moniker,
			voting_power = EXCLUDED.voting_power,
			updated_at = EXCLUDED.updated_at
	`
	_, err := s.db.ExecContext(ctx, query,
		validator.OperatorAddress, validator.ConsensusAddress, validator.ConsensusPubkey,
		validator.Jailed, validator.Status, validator.Tokens, validator.DelegatorShares,
		validator.Moniker, validator.Identity, validator.Website, validator.SecurityContact,
		validator.Details, validator.UnbondingHeight, validator.UnbondingTime,
		validator.CommissionRate, validator.CommissionMaxRate, validator.CommissionMaxChangeRate,
		validator.MinSelfDelegation, validator.VotingPower, validator.UpdatedAt,
	)
	return err
}

// StoreValidators performs batch upsert inside a single transaction.
func (s *PostgresStorage) StoreValidators(ctx context.Context, validators []*types.ParsedValidator) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, v := range validators {
		if err := s.StoreValidator(ctx, v); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *PostgresStorage) GetValidator(ctx context.Context, operatorAddr string) (*types.ParsedValidator, error) {
	query := `
		SELECT operator_address, consensus_address, consensus_pubkey, jailed, status,
			   tokens, delegator_shares, moniker, voting_power, updated_at
		FROM validators WHERE operator_address = $1
	`
	v := &types.ParsedValidator{}
	err := s.db.QueryRowContext(ctx, query, operatorAddr).Scan(
		&v.OperatorAddress, &v.ConsensusAddress, &v.ConsensusPubkey, &v.Jailed,
		&v.Status, &v.Tokens, &v.DelegatorShares, &v.Moniker, &v.VotingPower, &v.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (s *PostgresStorage) GetAllValidators(ctx context.Context) ([]*types.ParsedValidator, error) {
	query := `
		SELECT operator_address, consensus_address, status, jailed, voting_power, moniker
		FROM validators ORDER BY voting_power DESC
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var validators []*types.ParsedValidator
	for rows.Next() {
		v := &types.ParsedValidator{}
		err := rows.Scan(&v.OperatorAddress, &v.ConsensusAddress, &v.Status,
			&v.Jailed, &v.VotingPower, &v.Moniker)
		if err != nil {
			return nil, err
		}
		validators = append(validators, v)
	}
	return validators, nil
}

func (s *PostgresStorage) GetValidatorSyncState(ctx context.Context) (*types.ValidatorSyncState, error) {
	query := `SELECT last_sync_height, last_sync_time, total_validators FROM validator_sync_state WHERE id = 1`
	state := &types.ValidatorSyncState{}
	err := s.db.QueryRowContext(ctx, query).Scan(&state.LastSyncHeight, &state.LastSyncTime, &state.TotalValidators)
	if err == sql.ErrNoRows {
		return &types.ValidatorSyncState{}, nil
	}
	return state, err
}

func (s *PostgresStorage) UpdateValidatorSyncState(ctx context.Context, height int64) error {
	query := `
		INSERT INTO validator_sync_state (id, last_sync_height, last_sync_time)
		VALUES (1, $1, $2)
		ON CONFLICT (id) DO UPDATE SET
			last_sync_height = EXCLUDED.last_sync_height,
			last_sync_time = EXCLUDED.last_sync_time
	`
	_, err := s.db.ExecContext(ctx, query, height, time.Now())
	return err
}

// ============================================================================
// DELEGATION OPERATIONS
// ============================================================================
// Delegations are stored as latest state snapshots (not historical diffs).
// StoreDelegation upserts the delegator→validator relationship.

func (s *PostgresStorage) StoreDelegation(ctx context.Context, delegation *types.ParsedDelegation) error {
	query := `
		INSERT INTO delegations (
			delegator_address, validator_address, shares, amount, denom, height, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (delegator_address, validator_address) DO UPDATE SET
			shares = EXCLUDED.shares,
			amount = EXCLUDED.amount,
			height = EXCLUDED.height,
			updated_at = EXCLUDED.updated_at
	`
	_, err := s.db.ExecContext(ctx, query,
		delegation.DelegatorAddress, delegation.ValidatorAddress,
		delegation.Shares, delegation.Amount, delegation.Denom,
		delegation.Height, delegation.UpdatedAt,
	)
	return err
}

func (s *PostgresStorage) StoreDelegations(ctx context.Context, delegations []*types.ParsedDelegation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, d := range delegations {
		if err := s.StoreDelegation(ctx, d); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *PostgresStorage) GetDelegationsByDelegator(ctx context.Context, delegator string) ([]*types.ParsedDelegation, error) {
	query := `
		SELECT delegator_address, validator_address, shares, amount, denom, height, updated_at
		FROM delegations WHERE delegator_address = $1
	`
	rows, err := s.db.QueryContext(ctx, query, delegator)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var delegations []*types.ParsedDelegation
	for rows.Next() {
		d := &types.ParsedDelegation{}
		err := rows.Scan(&d.DelegatorAddress, &d.ValidatorAddress, &d.Shares,
			&d.Amount, &d.Denom, &d.Height, &d.UpdatedAt)
		if err != nil {
			return nil, err
		}
		delegations = append(delegations, d)
	}
	return delegations, nil
}

func (s *PostgresStorage) GetDelegationsByValidator(ctx context.Context, validator string) ([]*types.ParsedDelegation, error) {
	query := `
		SELECT delegator_address, validator_address, shares, amount, denom, height, updated_at
		FROM delegations WHERE validator_address = $1
	`
	rows, err := s.db.QueryContext(ctx, query, validator)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var delegations []*types.ParsedDelegation
	for rows.Next() {
		d := &types.ParsedDelegation{}
		err := rows.Scan(&d.DelegatorAddress, &d.ValidatorAddress, &d.Shares,
			&d.Amount, &d.Denom, &d.Height, &d.UpdatedAt)
		if err != nil {
			return nil, err
		}
		delegations = append(delegations, d)
	}
	return delegations, nil
}

// ============================================================================
// BALANCE OPERATIONS
// ============================================================================
// Balances are stored as latest known state per (address, denom).

func (s *PostgresStorage) StoreBalance(ctx context.Context, balance *types.ParsedBalance) error {
	query := `
		INSERT INTO balances (address, denom, amount, height, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (address, denom) DO UPDATE SET
			amount = EXCLUDED.amount,
			height = EXCLUDED.height,
			updated_at = EXCLUDED.updated_at
	`
	_, err := s.db.ExecContext(ctx, query,
		balance.Address, balance.Denom, balance.Amount, balance.Height, balance.UpdatedAt,
	)
	return err
}

func (s *PostgresStorage) StoreBalances(ctx context.Context, balances []*types.ParsedBalance) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, b := range balances {
		if err := s.StoreBalance(ctx, b); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *PostgresStorage) GetBalance(ctx context.Context, address, denom string) (*types.ParsedBalance, error) {
	query := `SELECT address, denom, amount, height, updated_at FROM balances WHERE address = $1 AND denom = $2`
	b := &types.ParsedBalance{}
	err := s.db.QueryRowContext(ctx, query, address, denom).Scan(
		&b.Address, &b.Denom, &b.Amount, &b.Height, &b.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (s *PostgresStorage) GetAllBalances(ctx context.Context, address string) ([]*types.ParsedBalance, error) {
	query := `SELECT address, denom, amount, height, updated_at FROM balances WHERE address = $1`
	rows, err := s.db.QueryContext(ctx, query, address)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var balances []*types.ParsedBalance
	for rows.Next() {
		b := &types.ParsedBalance{}
		err := rows.Scan(&b.Address, &b.Denom, &b.Amount, &b.Height, &b.UpdatedAt)
		if err != nil {
			return nil, err
		}
		balances = append(balances, b)
	}
	return balances, nil
}

// ============================================================================
// PROPOSAL OPERATIONS
// ============================================================================
// Governance proposals are periodically synced and updated
// as voting progresses.
// StoreProposal upserts governance proposal state,
// including live vote tallies and metadata.

func (s *PostgresStorage) StoreProposal(ctx context.Context, proposal *types.ParsedProposal) error {
	query := `
		INSERT INTO proposals (
			proposal_id, title, description, proposal_type, status,
			submit_time, deposit_end_time, voting_start_time, voting_end_time,
			total_deposit, deposit_denom, metadata, messages,
			yes_votes, no_votes, abstain_votes, no_with_veto_votes,
			proposer, height, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		ON CONFLICT (proposal_id) DO UPDATE SET
			status = EXCLUDED.status,
			total_deposit = EXCLUDED.total_deposit,
			deposit_denom = EXCLUDED.deposit_denom,
			metadata = EXCLUDED.metadata,
			messages = EXCLUDED.messages,
			yes_votes = EXCLUDED.yes_votes,
			no_votes = EXCLUDED.no_votes,
			abstain_votes = EXCLUDED.abstain_votes,
			no_with_veto_votes = EXCLUDED.no_with_veto_votes,
			updated_at = EXCLUDED.updated_at
	`
	_, err := s.db.ExecContext(ctx, query,
		proposal.ProposalID, proposal.Title, proposal.Description, proposal.ProposalType,
		proposal.Status, proposal.SubmitTime, proposal.DepositEndTime,
		proposal.VotingStartTime, proposal.VotingEndTime, proposal.TotalDeposit,
		proposal.DepositDenom, proposal.Metadata, proposal.Messages, // ← 3 new fields
		proposal.YesVotes, proposal.NoVotes, proposal.AbstainVotes, proposal.NoWithVetoVotes,
		proposal.Proposer, proposal.Height, proposal.UpdatedAt,
	)
	return err
}

func (s *PostgresStorage) StoreProposals(ctx context.Context, proposals []*types.ParsedProposal) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, p := range proposals {
		if err := s.StoreProposal(ctx, p); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *PostgresStorage) GetProposal(ctx context.Context, proposalID uint64) (*types.ParsedProposal, error) {
	query := `
		SELECT proposal_id, title, status, submit_time, voting_end_time, 
		       yes_votes, no_votes, abstain_votes, no_with_veto_votes
		FROM proposals WHERE proposal_id = $1
	`
	p := &types.ParsedProposal{}
	err := s.db.QueryRowContext(ctx, query, proposalID).Scan(
		&p.ProposalID, &p.Title, &p.Status, &p.SubmitTime, &p.VotingEndTime,
		&p.YesVotes, &p.NoVotes, &p.AbstainVotes, &p.NoWithVetoVotes,
	)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *PostgresStorage) GetActiveProposals(ctx context.Context) ([]*types.ParsedProposal, error) {
	query := `
		SELECT proposal_id, title, status, submit_time, voting_end_time
		FROM proposals 
		WHERE status IN ('PROPOSAL_STATUS_VOTING_PERIOD', 'PROPOSAL_STATUS_DEPOSIT_PERIOD')
		ORDER BY proposal_id DESC
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var proposals []*types.ParsedProposal
	for rows.Next() {
		p := &types.ParsedProposal{}
		err := rows.Scan(&p.ProposalID, &p.Title, &p.Status, &p.SubmitTime, &p.VotingEndTime)
		if err != nil {
			return nil, err
		}
		proposals = append(proposals, p)
	}
	return proposals, nil
}

func (s *PostgresStorage) GetProposalSyncState(ctx context.Context) (*types.ProposalSyncState, error) {
	query := `SELECT last_sync_height, last_sync_time, active_proposals FROM proposal_sync_state WHERE id = 1`
	state := &types.ProposalSyncState{}
	err := s.db.QueryRowContext(ctx, query).Scan(&state.LastSyncHeight, &state.LastSyncTime, &state.ActiveProposals)
	if err == sql.ErrNoRows {
		return &types.ProposalSyncState{}, nil
	}
	return state, err
}

func (s *PostgresStorage) UpdateProposalSyncState(ctx context.Context, height int64) error {
	query := `
		INSERT INTO proposal_sync_state (id, last_sync_height, last_sync_time)
		VALUES (1, $1, $2)
		ON CONFLICT (id) DO UPDATE SET
			last_sync_height = EXCLUDED.last_sync_height,
			last_sync_time = EXCLUDED.last_sync_time
	`
	_, err := s.db.ExecContext(ctx, query, height, time.Now())
	return err
}

// ============================================================================
// VOTE OPERATIONS (NEW)
// ============================================================================

func (s *PostgresStorage) StoreVote(ctx context.Context, vote *types.ParsedVote) error {
	optionsJSON, _ := json.Marshal(vote.Options)

	query := `
		INSERT INTO votes (
			proposal_id, voter, option, options, height, tx_hash, timestamp
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (proposal_id, voter) DO UPDATE SET
			option = EXCLUDED.option,
			options = EXCLUDED.options,
			height = EXCLUDED.height
	`
	_, err := s.db.ExecContext(ctx, query,
		vote.ProposalID, vote.Voter, vote.Option, optionsJSON,
		vote.Height, vote.TxHash, vote.Timestamp,
	)
	return err
}

func (s *PostgresStorage) StoreVotes(ctx context.Context, votes []*types.ParsedVote) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, v := range votes {
		if err := s.StoreVote(ctx, v); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *PostgresStorage) GetVotesByProposal(ctx context.Context, proposalID uint64) ([]*types.ParsedVote, error) {
	query := `
		SELECT proposal_id, voter, option, height, timestamp
		FROM votes WHERE proposal_id = $1
		ORDER BY timestamp DESC
	`
	rows, err := s.db.QueryContext(ctx, query, proposalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var votes []*types.ParsedVote
	for rows.Next() {
		v := &types.ParsedVote{}
		err := rows.Scan(&v.ProposalID, &v.Voter, &v.Option, &v.Height, &v.Timestamp)
		if err != nil {
			return nil, err
		}
		votes = append(votes, v)
	}
	return votes, nil
}

func (s *PostgresStorage) GetVotesByVoter(ctx context.Context, voter string) ([]*types.ParsedVote, error) {
	query := `
		SELECT proposal_id, voter, option, height, timestamp
		FROM votes WHERE voter = $1
		ORDER BY timestamp DESC
	`
	rows, err := s.db.QueryContext(ctx, query, voter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var votes []*types.ParsedVote
	for rows.Next() {
		v := &types.ParsedVote{}
		err := rows.Scan(&v.ProposalID, &v.Voter, &v.Option, &v.Height, &v.Timestamp)
		if err != nil {
			return nil, err
		}
		votes = append(votes, v)
	}
	return votes, nil
}
// upsertBalanceDelta adds or subtracts from a balance atomically inside a tx.
func (s *PostgresStorage) upsertBalanceDelta(ctx context.Context, dbTx *sql.Tx, address, denom, amountDelta string, height int64, add bool) error {
    var query string
    if add {
        query = `
            INSERT INTO balances (address, denom, amount, height, updated_at)
            VALUES ($1, $2, $3, $4, NOW())
            ON CONFLICT (address, denom) DO UPDATE SET
                amount = (CAST(balances.amount AS NUMERIC) + CAST($3 AS NUMERIC))::TEXT,
                height = EXCLUDED.height,
                updated_at = NOW()
        `
    } else {
        query = `
            INSERT INTO balances (address, denom, amount, height, updated_at)
            VALUES ($1, $2, '0', $4, NOW())
            ON CONFLICT (address, denom) DO UPDATE SET
                amount = GREATEST(0, (CAST(balances.amount AS NUMERIC) - CAST($3 AS NUMERIC)))::TEXT,
                height = EXCLUDED.height,
                updated_at = NOW()
        `
    }
    _, err := dbTx.ExecContext(ctx, query, address, denom, amountDelta, height)
    return err
}
// Close gracefully shuts down the database connection pool.
func (s *PostgresStorage) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
