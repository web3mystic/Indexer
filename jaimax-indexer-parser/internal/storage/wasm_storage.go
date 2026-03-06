package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cosmos-indexer/pkg/types"
)

// ============================================================================
// WASM EXECUTIONS
// ============================================================================
// StoreWasmExecution persists a MsgExecuteContract record.
// The execute_msg and funds fields are stored as JSONB to preserve
// full message structure for later querying or decoding.
//
// Uses ON CONFLICT DO NOTHING to prevent duplicate inserts during re-indexing.
// execute_msg, execute_action, funds, gas_used, success, error, timestamp
func (s *PostgresStorage) StoreWasmExecution(ctx context.Context, exec *types.WasmExecution) error {
	msgJSON, err := json.Marshal(exec.ExecuteMsg)
	if err != nil {
		return fmt.Errorf("StoreWasmExecution: marshal execute_msg: %w", err)
	}
	fundsJSON, err := json.Marshal(exec.Funds)
	if err != nil {
		return fmt.Errorf("StoreWasmExecution: marshal funds: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO wasm_executions (
			tx_hash, msg_index, height,
			sender, contract_address,
			execute_msg, execute_action, funds,
			gas_used, success, error, timestamp
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT DO NOTHING`,
		exec.TxHash, exec.MsgIndex, exec.Height,
		exec.Sender, exec.ContractAddress,
		msgJSON, exec.ExecuteAction, fundsJSON,
		exec.GasUsed, exec.Success, exec.Error, exec.Timestamp,
	)
	return err
}

// ============================================================================
// WASM INSTANTIATIONS  —  table: wasm_instantiations
// ============================================================================
// StoreWasmExecution persists a MsgExecuteContract record.
// The execute_msg and funds fields are stored as JSONB to preserve
// full message structure for later querying or decoding.
//
// Uses ON CONFLICT DO NOTHING to prevent duplicate inserts
// during re-indexing.

// Columns: tx_hash, msg_index, height, creator, admin, code_id, label,
//
//	contract_address, init_msg, funds, success, error, timestamp
func (s *PostgresStorage) StoreWasmInstantiation(ctx context.Context, inst *types.WasmInstantiation) error {
	initJSON, err := json.Marshal(inst.InitMsg)
	if err != nil {
		return fmt.Errorf("StoreWasmInstantiation: marshal init_msg: %w", err)
	}
	fundsJSON, err := json.Marshal(inst.Funds)
	if err != nil {
		return fmt.Errorf("StoreWasmInstantiation: marshal funds: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO wasm_instantiations (
			tx_hash, msg_index, height,
			creator, admin, code_id, label, contract_address,
			init_msg, funds, success, error, timestamp
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT DO NOTHING`,
		inst.TxHash, inst.MsgIndex, inst.Height,
		inst.Creator, inst.Admin, inst.CodeID, inst.Label, inst.ContractAddress,
		initJSON, fundsJSON, inst.Success, inst.Error, inst.Timestamp,
	)
	return err
}

// ============================================================================
// WASM MIGRATIONS  —  table: wasm_migrations
// ============================================================================
// StoreWasmMigration persists a MsgMigrateContract record.
// The contract's current_code_id is updated by a DB trigger
// after successful migration insertion.
//
// Columns: tx_hash, msg_index, height, sender, contract_address,
//
//	old_code_id, new_code_id, migrate_msg, success, error, timestamp
func (s *PostgresStorage) StoreWasmMigration(ctx context.Context, mig *types.WasmMigration) error {
	migJSON, err := json.Marshal(mig.MigrateMsg)
	if err != nil {
		return fmt.Errorf("StoreWasmMigration: marshal migrate_msg: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO wasm_migrations (
			tx_hash, msg_index, height,
			sender, contract_address,
			old_code_id, new_code_id, migrate_msg,
			success, error, timestamp
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT DO NOTHING`,
		mig.TxHash, mig.MsgIndex, mig.Height,
		mig.Sender, mig.ContractAddress,
		mig.OldCodeID, mig.NewCodeID, migJSON,
		mig.Success, mig.Error, mig.Timestamp,
	)
	return err
}

// ============================================================================
// WASM EVENTS  —  table: wasm_events
// ============================================================================
// StoreWasmEvent stores a single CosmWasm event emitted during execution.
// raw_attributes are stored as JSONB to allow flexible event queries.
//
// Columns: tx_hash, msg_index, event_index, height,
//
//	contract_address, action, raw_attributes, timestamp
func (s *PostgresStorage) StoreWasmEvent(ctx context.Context, event *types.WasmEvent) error {
	attrsJSON, err := json.Marshal(event.RawAttributes)
	if err != nil {
		return fmt.Errorf("StoreWasmEvent: marshal attributes: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO wasm_events (
			tx_hash, msg_index, event_index, height,
			contract_address, action, raw_attributes, timestamp
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT DO NOTHING`,
		event.TxHash, event.MsgIndex, event.EventIndex, event.Height,
		event.ContractAddress, event.Action, attrsJSON, event.Timestamp,
	)
	return err
}

// StoreWasmEvents performs batch insertion inside a single transaction
// for improved performance when processing high-event transactions.
func (s *PostgresStorage) StoreWasmEvents(ctx context.Context, events []*types.WasmEvent) error {
	if len(events) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("StoreWasmEvents: begin tx: %w", err)
	}
	// Rollback is safe even after Commit; it will no-op if already committed.
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO wasm_events (
			tx_hash, msg_index, event_index, height,
			contract_address, action, raw_attributes, timestamp
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return fmt.Errorf("StoreWasmEvents: prepare: %w", err)
	}
	defer stmt.Close()

	for _, e := range events {
		// Marshal errors are ignored here intentionally since attributes
		// are non-critical and already validated upstream.
		attrsJSON, _ := json.Marshal(e.RawAttributes)
		if _, err := stmt.ExecContext(ctx,
			e.TxHash, e.MsgIndex, e.EventIndex, e.Height,
			e.ContractAddress, e.Action, attrsJSON, e.Timestamp,
		); err != nil {
			return fmt.Errorf("StoreWasmEvents: exec: %w", err)
		}
	}

	return tx.Commit()
}

// ============================================================================
// CW20 TRANSFERS  —  table: cw20_transfers
// ============================================================================
// StoreCW20Transfer inserts a normalized CW20 token transfer.
// Supports transfer, send, mint, burn and other CW20 actions.
// raw_attributes are preserved for auditing purposes.
//
// Columns: tx_hash, msg_index, height, contract_address, action,
//
//	from_address, to_address, amount, memo, raw_attributes, timestamp
func (s *PostgresStorage) StoreCW20Transfer(ctx context.Context, t *types.CW20Transfer) error {
	attrsJSON, _ := json.Marshal(t.RawAttributes)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cw20_transfers (
			tx_hash, msg_index, height,
			contract_address, action,
			from_address, to_address, amount, memo,
			raw_attributes, timestamp
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT DO NOTHING`,
		t.TxHash, t.MsgIndex, t.Height,
		t.ContractAddress, t.Action,
		t.FromAddress, t.ToAddress, t.Amount, t.Memo,
		attrsJSON, t.Timestamp,
	)
	return err
}

// StoreCW20Transfers batches CW20 transfer inserts
// to reduce round-trips and improve indexing throughput.
func (s *PostgresStorage) StoreCW20Transfers(ctx context.Context, ts []*types.CW20Transfer) error {
	if len(ts) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("StoreCW20Transfers: begin tx: %w", err)
	}
	defer tx.Rollback() // Rollback ensures atomicity if any insert fails.

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO cw20_transfers (
			tx_hash, msg_index, height,
			contract_address, action,
			from_address, to_address, amount, memo,
			raw_attributes, timestamp
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return fmt.Errorf("StoreCW20Transfers: prepare: %w", err)
	}
	defer stmt.Close()

	for _, t := range ts {
		attrsJSON, _ := json.Marshal(t.RawAttributes)
		if _, err := stmt.ExecContext(ctx,
			t.TxHash, t.MsgIndex, t.Height,
			t.ContractAddress, t.Action,
			t.FromAddress, t.ToAddress, t.Amount, t.Memo,
			attrsJSON, t.Timestamp,
		); err != nil {
			return fmt.Errorf("StoreCW20Transfers: exec: %w", err)
		}
	}

	return tx.Commit()
}

// ============================================================================
// BANK TRANSFERS  —  table: bank_transfers
// ============================================================================
// StoreBankTransfer persists a native SDK bank transfer event.
// amount_value is stored separately for numeric sorting and aggregation.
// Columns: tx_hash, msg_index, height,
//
//	from_address, to_address, amount, denom, amount_value, timestamp
func (s *PostgresStorage) StoreBankTransfer(ctx context.Context, t *types.BankTransfer) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO bank_transfers (
			tx_hash, msg_index, height,
			from_address, to_address,
			amount, denom, amount_value, timestamp
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT DO NOTHING`,
		t.TxHash, t.MsgIndex, t.Height,
		t.FromAddress, t.ToAddress,
		t.Amount, t.Denom, t.AmountValue, t.Timestamp,
	)
	return err
}

// StoreBankTransfers performs bulk insertion of native token transfers
// inside a single DB transaction for efficiency.
func (s *PostgresStorage) StoreBankTransfers(ctx context.Context, ts []*types.BankTransfer) error {
	if len(ts) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("StoreBankTransfers: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO bank_transfers (
			tx_hash, msg_index, height,
			from_address, to_address,
			amount, denom, amount_value, timestamp
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return fmt.Errorf("StoreBankTransfers: prepare: %w", err)
	}
	defer stmt.Close()

	for _, t := range ts {
		if _, err := stmt.ExecContext(ctx,
			t.TxHash, t.MsgIndex, t.Height,
			t.FromAddress, t.ToAddress,
			t.Amount, t.Denom, t.AmountValue, t.Timestamp,
		); err != nil {
			return fmt.Errorf("StoreBankTransfers: exec: %w", err)
		}
	}

	return tx.Commit()
}

// ============================================================================
// CONTRACT REGISTRY  —  table: wasm_contracts
// Maintains the latest known state of each contract.
// This table acts as a canonical registry for contract metadata.
// ============================================================================
// UpsertWasmContract inserts or updates a contract registry entry.
//
// Only mutable fields (admin, current_code_id, is_active) are updated
// on conflict to preserve original instantiation metadata.

func (s *PostgresStorage) UpsertWasmContract(ctx context.Context, c *types.WasmContract) error {
	initJSON, _ := json.Marshal(c.InitMsg)
	// Build contract_info from known fields if not explicitly provided
	if c.ContractInfo == nil {
		c.ContractInfo = map[string]interface{}{
			"code_id": c.CodeID,
			"creator": c.Creator,
			"admin":   c.Admin,
			"label":   c.Label,
		}
	}
	infoJSON, _ := json.Marshal(c.ContractInfo)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO wasm_contracts (
			contract_address, code_id, creator, admin, label,
			init_msg, contract_info,
			instantiated_at_height, instantiated_at_time, instantiate_tx_hash,
			current_code_id, is_active
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (contract_address) DO UPDATE SET
			admin           = EXCLUDED.admin,
			current_code_id = EXCLUDED.current_code_id,
			is_active       = EXCLUDED.is_active,
			updated_at      = NOW()`,
		c.ContractAddress, c.CodeID, c.Creator, c.Admin, c.Label,
		initJSON, infoJSON,
		c.InstantiatedAtHeight, c.InstantiatedAtTime, c.InstantiateTxHash,
		c.CurrentCodeID, c.IsActive,
	)
	return err
}

// GetWasmContract retrieves the current registry state for a contract.
// Primarily used during migrations to determine the previous code_id.
func (s *PostgresStorage) GetWasmContract(ctx context.Context, contractAddress string) (*types.WasmContract, error) {
	c := &types.WasmContract{}
	err := s.db.QueryRowContext(ctx, `
		SELECT contract_address, code_id, creator, admin, label,
		       instantiated_at_height, instantiated_at_time,
		       current_code_id, is_active
		FROM wasm_contracts
		WHERE contract_address = $1`,
		contractAddress,
	).Scan(
		&c.ContractAddress, &c.CodeID, &c.Creator, &c.Admin, &c.Label,
		&c.InstantiatedAtHeight, &c.InstantiatedAtTime,
		&c.CurrentCodeID, &c.IsActive,
	)
	if err != nil {
		return nil, fmt.Errorf("GetWasmContract %s: %w", contractAddress, err)
	}
	return c, nil
}

// ============================================================================
// WASM CODES  —  table: wasm_codes
// ============================================================================

// StoreWasmCode inserts a new wasm code upload record.
// Uses ON CONFLICT DO NOTHING — safe for re-indexing.
func (s *PostgresStorage) StoreWasmCode(ctx context.Context, c *types.WasmCode) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO wasm_codes (
			code_id, creator, checksum,
			permission,
			uploaded_height, uploaded_time, upload_tx_hash
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT DO NOTHING`,
		c.CodeID, c.Creator, c.Checksum,
		c.Permission,
		c.UploadedHeight, c.UploadedTime, c.UploadTxHash,
	)
	if err != nil {
		return fmt.Errorf("StoreWasmCode (code_id=%d): %w", c.CodeID, err)
	}
	return nil
}

// UpsertWasmCode inserts or updates a wasm code record.
// Only mutable fields (permission, permitted_addr) are updated on conflict.
func (s *PostgresStorage) UpsertWasmCode(ctx context.Context, c *types.WasmCode) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO wasm_codes (
			code_id, creator, checksum,
			permission,
			uploaded_height, uploaded_time, upload_tx_hash
		) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (code_id) DO UPDATE SET
			permission = EXCLUDED.permission`,
		c.CodeID, c.Creator, c.Checksum,
		c.Permission,
		c.UploadedHeight, c.UploadedTime, c.UploadTxHash,
	)
	if err != nil {
		return fmt.Errorf("UpsertWasmCode (code_id=%d): %w", c.CodeID, err)
	}
	return nil
}

// GetWasmCode retrieves a single wasm code record by code_id.
func (s *PostgresStorage) GetWasmCode(ctx context.Context, codeID int64) (*types.WasmCode, error) {
	c := &types.WasmCode{}
	err := s.db.QueryRowContext(ctx, `
		SELECT code_id, creator, checksum,
		       permission,
		       uploaded_height, uploaded_time, upload_tx_hash
		FROM wasm_codes
		WHERE code_id = $1`,
		codeID,
	).Scan(
		&c.CodeID, &c.Creator, &c.Checksum,
		&c.Permission,
		&c.UploadedHeight, &c.UploadedTime, &c.UploadTxHash,
	)
	if err != nil {
		return nil, fmt.Errorf("GetWasmCode (code_id=%d): %w", codeID, err)
	}
	return c, nil
}

// GetAllWasmCodes returns every wasm code record ordered by code_id ascending.
// Intended for explorer list endpoints — returns an empty slice (not nil)
// when the table is empty so callers can safely range over the result.
func (s *PostgresStorage) GetAllWasmCodes(ctx context.Context) ([]*types.WasmCode, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT code_id, creator, checksum,
		       permission, permitted_addr,
		       uploaded_height, uploaded_time, upload_tx_hash
		FROM wasm_codes
		ORDER BY code_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("GetAllWasmCodes: query: %w", err)
	}
	defer rows.Close()

	codes := make([]*types.WasmCode, 0)
	for rows.Next() {
		c := &types.WasmCode{}
		if err := rows.Scan(
			&c.CodeID, &c.Creator, &c.Checksum,
			&c.Permission,
			&c.UploadedHeight, &c.UploadedTime, &c.UploadTxHash,
		); err != nil {
			return nil, fmt.Errorf("GetAllWasmCodes: scan: %w", err)
		}
		codes = append(codes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GetAllWasmCodes: rows: %w", err)
	}
	return codes, nil
}
