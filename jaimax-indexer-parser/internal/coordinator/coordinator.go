package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/cosmos-indexer/internal/fetcher"
	"github.com/cosmos-indexer/internal/parser"
	"github.com/cosmos-indexer/internal/storage"
	"github.com/cosmos-indexer/pkg/config"
	"github.com/cosmos-indexer/pkg/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
)

// Coordinator orchestrates the full indexing lifecycle:
// fetch → parse → store → post-process.
type Coordinator struct {
	fetcher    fetcher.Fetcher    // Responsible for querying chain data (RPC/REST)
	parser     parser.Parser      // Converts raw chain data into structured models
	storage    storage.Storage    // Persists parsed data into database
	config     *config.Config     // Runtime configuration
	wasmParser *parser.WasmParser // Specialized parser for CosmWasm events/messages
}

// NewCoordinator wires all dependencies together.
func NewCoordinator(
	f fetcher.Fetcher,
	p parser.Parser,
	s storage.Storage,
	cfg *config.Config,
) *Coordinator {
	return &Coordinator{
		fetcher:    f,
		parser:     p,
		storage:    s,
		config:     cfg,
		wasmParser: parser.NewWasmParser(),
	}
}

// Start begins the continuous indexing loop.
// It resumes from the last indexed height if available.
func (c *Coordinator) Start(ctx context.Context) error {
	fmt.Println("Starting Enhanced Cosmos Indexer...")

	// Determine resume height from storage
	lastHeight, err := c.storage.GetLastIndexedHeight(ctx)
	if err != nil {
		return fmt.Errorf("failed to get last indexed height: %w", err)
	}

	startHeight := c.config.StartHeight
	if lastHeight > 0 {
		startHeight = lastHeight + 1 // resume safely from next block
		fmt.Printf("Resuming from height %d\n", startHeight)
	} else {
		fmt.Printf("Starting from height %d\n", startHeight)
	}

	// Get current chain tip
	latestHeight, err := c.fetcher.GetLatestHeight(ctx)
	if err != nil {
		return fmt.Errorf("failed to get latest height: %w", err)
	}

	fmt.Printf("Chain height: %d\n", latestHeight)

	// Initial state sync before block processing begins
	fmt.Println("Initial sync...")
	if err := c.syncValidators(ctx, startHeight); err != nil {
		fmt.Printf("Failed to sync validators: %v\n", err)
	}
	if err := c.syncProposals(ctx, startHeight); err != nil {
		fmt.Printf("Failed to sync proposals: %v\n", err)
	}

	currentHeight := startHeight

	// Main indexing loop
	for {
		select {
		case <-ctx.Done():
			fmt.Println("Indexer stopped")
			return ctx.Err()

		default:
			// If caught up, poll for new blocks
			if currentHeight > latestHeight {
				fmt.Println(" Caught up with chain, waiting...")
				time.Sleep(5 * time.Second)

				latestHeight, err = c.fetcher.GetLatestHeight(ctx)
				if err != nil {
					time.Sleep(5 * time.Second)
				}
				continue
			}

			// Process block with retry mechanism
			if err := c.indexBlockWithRetry(ctx, currentHeight); err != nil {
				fmt.Printf("Failed block %d: %v\n", currentHeight, err)
				time.Sleep(time.Duration(c.config.RetryDelayMs) * time.Millisecond)
				continue
			}

			// Periodic sync tasks
			if currentHeight%100 == 0 {
				if err := c.syncValidators(ctx, currentHeight); err != nil {
					fmt.Printf("Failed to sync validators at height %d: %v\n", currentHeight, err)
				}
			}

			if currentHeight%10 == 0 {
				if err := c.syncProposals(ctx, currentHeight); err != nil {
					fmt.Printf("Failed to sync proposals at height %d: %v\n", currentHeight, err)
				}
			}

			fmt.Printf("✓ Indexed block %d/%d\n", currentHeight, latestHeight)
			currentHeight++
		}
	}
}

// indexBlockWithRetry retries indexing in case of transient failures
// (network, RPC hiccups, temporary DB issues).
func (c *Coordinator) indexBlockWithRetry(ctx context.Context, height int64) error {
	var lastErr error

	for i := 0; i < c.config.RetryAttempts; i++ {
		err := c.indexBlock(ctx, height)
		if err == nil {
			return nil
		}

		lastErr = err
		time.Sleep(time.Duration(c.config.RetryDelayMs) * time.Millisecond)
	}

	return lastErr
}

// indexBlock performs the full pipeline for a single block:
// 1. Fetch block
// 2. Fetch tx responses
// 3. Parse
// 4. Store block + transactions
// 5. Execute post-processing handlers
func (c *Coordinator) indexBlock(ctx context.Context, height int64) error {
	// ===============================
	// FETCH BLOCK
	// ===============================
	rawBlock, err := c.fetcher.FetchBlock(ctx, height)
	if err != nil {
		return fmt.Errorf("fetch failed: %w", err)
	}

	// ===============================
	// FETCH TX RESPONSES
	// Each tx hash is SHA256(rawBytes)
	// ===============================
	rawBlock.TxResponses = make([]interface{}, len(rawBlock.RawTxs))

	for i, txBytes := range rawBlock.RawTxs {
		hash := sha256.Sum256(txBytes)
		hashHex := strings.ToUpper(hex.EncodeToString(hash[:]))

		txResp, err := c.fetcher.FetchTx(ctx, hashHex)
		if err != nil {
			// Continue — block should not fail due to a single tx response
			fmt.Printf("tx response fetch failed: %s\n", hashHex)
			continue
		}

		rawBlock.TxResponses[i] = txResp.TxResponse
	}

	// ===============================
	// PARSE BLOCK INTO STRUCTURED MODEL
	// ===============================
	parsedBlock, err := c.parser.ParseBlock(rawBlock)
	if err != nil {
		return fmt.Errorf("parse failed: %w", err)
	}

	// ===============================
	// STORE BLOCK FIRST
	// IMPORTANT:
	// transactions(hash) must exist before
	// wasm_* tables insert due to FK constraints.
	// ===============================
	if err := c.storage.StoreBlock(ctx, parsedBlock); err != nil {
		return fmt.Errorf("storage failed: %w", err)
	}

	// ===============================
	// HANDLE TX-SPECIFIC UPDATES
	// Post-processing handlers
	// These update derived state (delegations, votes, balances, wasm ops)
	// ===============================
	for i := range parsedBlock.Txs {
		tx := &parsedBlock.Txs[i]

		// Governance / staking / bank triggers
		if c.containsMessageType(tx.MsgTypes, "MsgDelegate") {
			if err := c.handleDelegateMessage(ctx, tx, height); err != nil {
				fmt.Printf("Failed to handle delegate message: %v\n", err)
			}
		}

		if c.containsMessageType(tx.MsgTypes, "MsgVote") {
			if err := c.handleVoteMessage(ctx, tx); err != nil {
				fmt.Printf("Failed to handle vote message: %v\n", err)
			}
		}

		if c.containsMessageType(tx.MsgTypes, "MsgSend") {
			if err := c.handleTransferMessage(ctx, tx, height); err != nil {
				fmt.Printf("Failed to handle transfer message: %v\n", err)
			}
		}

		// Decode raw protobuf for wasm-specific message inspection
		if int(tx.Index) < len(rawBlock.RawTxs) {
			if err := c.handleWasmAndBankTxs(ctx, tx, rawBlock.RawTxs[tx.Index]); err != nil {
				fmt.Printf("Failed to handle wasm/bank tx %s: %v\n", tx.Hash, err)
			}
		}
	}

	return nil
}

// ============================================================================
// VALIDATOR SYNC (every 100 blocks)
// ============================================================================
// syncValidators refreshes full validator set snapshot.
// Triggered periodically (every 100 blocks).
// This avoids needing to track per-event validator changes.
func (c *Coordinator) syncValidators(ctx context.Context, height int64) error {
	fmt.Printf("Syncing validators at height %d...\n", height)

	// Fetch validators from chain
	rawValidators, err := c.fetcher.FetchValidators(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch validators: %w", err)
	}

	// Parse validators
	parsedValidators, err := c.parser.ParseValidators(rawValidators)
	if err != nil {
		return fmt.Errorf("failed to parse validators: %w", err)
	}

	// Store validators
	if err := c.storage.StoreValidators(ctx, parsedValidators); err != nil {
		return fmt.Errorf("failed to store validators: %w", err)
	}

	// Update sync state
	if err := c.storage.UpdateValidatorSyncState(ctx, height); err != nil {
		return fmt.Errorf("failed to update validator sync state: %w", err)
	}

	fmt.Printf("Synced %d validators\n", len(parsedValidators))
	return nil
}

// ============================================================================
// PROPOSAL SYNC (every 10 blocks)
// ============================================================================
// syncProposals refreshes governance proposals and
// syncs votes for proposals currently in voting period.
// Triggered every 10 blocks.
func (c *Coordinator) syncProposals(ctx context.Context, height int64) error {
	fmt.Printf("Syncing proposals at height %d...\n", height)

	// Fetch proposals from chain
	rawProposals, err := c.fetcher.FetchProposals(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch proposals: %w", err)
	}

	// Parse proposals
	parsedProposals, err := c.parser.ParseProposals(rawProposals, height)
	if err != nil {
		return fmt.Errorf("failed to parse proposals: %w", err)
	}

	// Store proposals
	if err := c.storage.StoreProposals(ctx, parsedProposals); err != nil {
		return fmt.Errorf("failed to store proposals: %w", err)
	}

	// Sync votes for active proposals
	for _, proposal := range parsedProposals {
		if proposal.Status == "PROPOSAL_STATUS_VOTING_PERIOD" {
			if err := c.syncProposalVotes(ctx, proposal.ProposalID, height); err != nil {
				fmt.Printf("Failed to sync votes for proposal %d: %v\n", proposal.ProposalID, err)
			}
		}
	}

	// Update sync state
	if err := c.storage.UpdateProposalSyncState(ctx, height); err != nil {
		return fmt.Errorf("failed to update proposal sync state: %w", err)
	}

	fmt.Printf("Synced %d proposals\n", len(parsedProposals))
	return nil
}

func (c *Coordinator) syncProposalVotes(ctx context.Context, proposalID uint64, height int64) error {
	// Fetch votes
	rawVotes, err := c.fetcher.FetchVotes(ctx, proposalID)
	if err != nil {
		return err
	}

	// Parse votes
	parsedVotes, err := c.parser.ParseVotes(rawVotes, height)
	if err != nil {
		return err
	}

	// Store votes
	return c.storage.StoreVotes(ctx, parsedVotes)
}

// ============================================================================
// MESSAGE HANDLERS (triggered by transactions)
// ============================================================================

func (c *Coordinator) handleDelegateMessage(ctx context.Context, tx *types.ParsedTx, height int64) error {
	// Extract delegation info from messages
	for _, msg := range tx.Messages {
		if !strings.Contains(msg.Type, "MsgDelegate") {
			continue
		}

		delegator := msg.Sender

		// Fetch updated delegations for this delegator
		rawDelegations, err := c.fetcher.FetchDelegationsByDelegator(ctx, delegator)
		if err != nil {
			return err
		}

		// Parse and store
		parsedDelegations, err := c.parser.ParseDelegations(rawDelegations, height)
		if err != nil {
			return err
		}

		if err := c.storage.StoreDelegations(ctx, parsedDelegations); err != nil {
			return err
		}

		fmt.Printf("Updated %d delegations for %s\n", len(parsedDelegations), delegator)
	}

	return nil
}

func (c *Coordinator) handleVoteMessage(ctx context.Context, tx *types.ParsedTx) error {
	// Extract vote info from messages
	for _, msg := range tx.Messages {
		if !strings.Contains(msg.Type, "MsgVote") {
			continue
		}

		if proposalID, ok := msg.RawData["proposal_id"].(uint64); ok {
			voter := msg.Sender
			option := msg.RawData["option"].(string)
			hash := tx.Hash
			// Create vote
			vote := &types.ParsedVote{
				ProposalID: proposalID,
				Voter:      voter,
				Option:     option,
				Height:     tx.Height,
				TxHash:     &hash,
				Timestamp:  tx.Timestamp,
			}

			if err := c.storage.StoreVote(ctx, vote); err != nil {
				return err
			}

			fmt.Printf("Recorded vote from %s on proposal %d\n", voter, proposalID)
		}
	}

	return nil
}

func (c *Coordinator) handleTransferMessage(ctx context.Context, tx *types.ParsedTx, height int64) error {
	// Extract addresses from transfer messages
	addresses := make(map[string]bool)

	for _, msg := range tx.Messages {
		if !strings.Contains(msg.Type, "MsgSend") {
			continue
		}

		addresses[msg.Sender] = true
		addresses[msg.Receiver] = true
	}

	// Fetch and update balances for involved addresses
	for addr := range addresses {
		rawBalances, err := c.fetcher.FetchBalances(ctx, addr)
		if err != nil {
			fmt.Printf("Failed to fetch balances for %s: %v\n", addr, err)
			continue
		}

		parsedBalances, err := c.parser.ParseBalances(rawBalances, height)
		if err != nil {
			continue
		}

		if err := c.storage.StoreBalances(ctx, parsedBalances); err != nil {
			fmt.Printf("Failed to store balances for %s: %v\n", addr, err)
		}
	}

	return nil
}

// ============================================================================
// HELPER METHODS
// handleWasmAndBankTxs
// Processes every CosmWasm message and native bank transfers in a tx.
// Called AFTER StoreBlock so transactions(hash) FK parent row already exists.
// ============================================================================

func (c *Coordinator) handleWasmAndBankTxs(ctx context.Context, tx *types.ParsedTx, txRawBytes []byte) error {
	ts := tx.Timestamp
	height := tx.Height
	txHash := tx.Hash
	success := tx.Success
	errMsg := ""
	if !success {
		errMsg = tx.Log
	}

	// 1. Store raw wasm events (instantiate / execute / migrate / wasm / reply)
	wasmEvents := c.wasmParser.ExtractWasmEvents(tx.Events, txHash, height, ts)
	if len(wasmEvents) > 0 {
		if err := c.storage.StoreWasmEvents(ctx, wasmEvents); err != nil {
			fmt.Printf("StoreWasmEvents tx %s: %v\n", txHash, err)
		}
	}

	// 2. CW20 transfers derived from wasm events
	cw20Transfers, err := c.wasmParser.ParseCW20Transfers(wasmEvents, txHash, height, ts)
	if err == nil && len(cw20Transfers) > 0 {
		if err := c.storage.StoreCW20Transfers(ctx, cw20Transfers); err != nil {
			fmt.Printf("StoreCW20Transfers tx %s: %v\n", txHash, err)
		}
	}

	// 3. Native bank transfers from tx events
	bankTransfers, err := c.wasmParser.ParseBankTransfers(tx.Events, txHash, 0, height, ts)
	if err == nil && len(bankTransfers) > 0 {
		if err := c.storage.StoreBankTransfers(ctx, bankTransfers); err != nil {
			fmt.Printf("StoreBankTransfers tx %s: %v\n", txHash, err)
		}
	}

	// 4. Per-message wasm operations (decode from raw tx bytes)
	if len(txRawBytes) == 0 {
		return nil
	}

	msgSlice := extractRawMessages(txRawBytes)
	for msgIndex, rawMsg := range msgSlice {
		typeURL := rawMsg.TypeURL
		value := rawMsg.Value

		switch {
		case strings.Contains(typeURL, "MsgStoreCode"):
			codeID := parser.ExtractCodeIDFromEvents(tx.Events)
			if codeID == 0 {
				fmt.Printf("MsgStoreCode tx %s: could not extract code_id from events, skipping\n", txHash)
				continue
			}
			code, err := c.wasmParser.ParseStoreCode(
				value, txHash, height, codeID, ts, tx.Events,
			)
			if err != nil {
				fmt.Printf("ParseStoreCode tx %s msg %d: %v\n", txHash, msgIndex, err)
				continue
			}
			if err := c.storage.StoreWasmCode(ctx, code); err != nil {
				fmt.Printf("StoreWasmCode tx %s: %v\n", txHash, err)
			} else {
				fmt.Printf("Stored wasm code code_id=%d creator=%s tx=%s\n",
					code.CodeID, code.Creator, txHash)
			}

		case strings.Contains(typeURL, "MsgInstantiateContract"):
			inst, err := c.wasmParser.ParseInstantiateContract(
				value, txHash, msgIndex, height,
				success, errMsg, ts, tx.Events,
			)
			if err != nil {
				fmt.Printf("ParseInstantiateContract tx %s msg %d: %v\n", txHash, msgIndex, err)
				continue
			}
			if err := c.storage.StoreWasmInstantiation(ctx, inst); err != nil {
				fmt.Printf("StoreWasmInstantiation tx %s: %v\n", txHash, err)
			} else {
				fmt.Printf("Stored instantiation contract=%s code_id=%d tx=%s\n",
					inst.ContractAddress, inst.CodeID, txHash)
			}

		case strings.Contains(typeURL, "MsgExecuteContract"):
			exec, err := c.wasmParser.ParseExecuteContract(
				value, txHash, msgIndex, height,
				tx.GasUsed, success, errMsg, ts,
			)
			if err != nil {
				fmt.Printf("ParseExecuteContract tx %s msg %d: %v\n", txHash, msgIndex, err)
				continue
			}
			if err := c.storage.StoreWasmExecution(ctx, exec); err != nil {
				fmt.Printf("StoreWasmExecution tx %s: %v\n", txHash, err)
			} else {
				fmt.Printf("Stored execution contract=%s action=%s tx=%s\n",
					exec.ContractAddress, exec.ExecuteAction, txHash)
			}

		case strings.Contains(typeURL, "MsgMigrateContract"):
			mig, err := c.wasmParser.ParseMigrateContract(
				value, txHash, msgIndex, height,
				success, errMsg, ts,
			)
			if err != nil {
				fmt.Printf("ParseMigrateContract tx %s msg %d: %v\n", txHash, msgIndex, err)
				continue
			}
			if existing, dbErr := c.storage.GetWasmContract(ctx, mig.ContractAddress); dbErr == nil {
				mig.OldCodeID = existing.CurrentCodeID
			}
			if err := c.storage.StoreWasmMigration(ctx, mig); err != nil {
				fmt.Printf("StoreWasmMigration tx %s: %v\n", txHash, err)
			} else {
				fmt.Printf("Stored migration contract=%s %d->%d tx=%s\n",
					mig.ContractAddress, mig.OldCodeID, mig.NewCodeID, txHash)
			}
		}
	}
	return nil
}

// extractRawMessages decodes protobuf tx bytes.
func extractRawMessages(txBytes []byte) []struct {
	TypeURL string
	Value   []byte
} {
	var tx txtypes.Tx
	if err := tx.Unmarshal(txBytes); err != nil {
		return nil
	}
	if tx.Body == nil {
		return nil
	}
	out := make([]struct {
		TypeURL string
		Value   []byte
	}, len(tx.Body.Messages))
	for i, m := range tx.Body.Messages {
		out[i].TypeURL = m.TypeUrl
		out[i].Value = m.Value
	}
	return out
}

func (c *Coordinator) containsMessageType(msgTypes []string, searchType string) bool {
	for _, msgType := range msgTypes {
		if strings.Contains(msgType, searchType) {
			return true
		}
	}
	return false
}
