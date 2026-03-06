package parser

// Package parser transforms raw Cosmos SDK RPC/ABCI responses
// into normalized, storage-ready indexer models.
//
// It decodes blocks, transactions, messages, and events,
// providing a chain-agnostic parsing layer between
// the node RPC and the storage layer.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cosmos-indexer/pkg/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	gogoany "github.com/cosmos/gogoproto/types/any"
)

type Parser interface {
	// Block parsing
	ParseBlock(raw *types.RawBlock) (*types.ParsedBlock, error)
	ParseTx(txBytes []byte, height int64, index int32, txResp interface{}) (*types.ParsedTx, error)

	// Validator parsing
	ParseValidator(raw *types.RawValidator) (*types.ParsedValidator, error)
	ParseValidators(raws []*types.RawValidator) ([]*types.ParsedValidator, error)

	// Delegation parsing
	ParseDelegation(raw *types.RawDelegation, height int64) (*types.ParsedDelegation, error)
	ParseDelegations(raws []*types.RawDelegation, height int64) ([]*types.ParsedDelegation, error)

	// Balance parsing
	ParseBalance(raw *types.RawBalance, height int64) (*types.ParsedBalance, error)
	ParseBalances(raws []*types.RawBalance, height int64) ([]*types.ParsedBalance, error)

	// Proposal parsing
	ParseProposal(raw *types.RawProposal, height int64) (*types.ParsedProposal, error)
	ParseProposals(raws []*types.RawProposal, height int64) ([]*types.ParsedProposal, error)

	// Vote parsing
	ParseVote(raw *types.RawVote, height int64, txHash *string, timestamp time.Time) (*types.ParsedVote, error)
	ParseVotes(raws []*types.RawVote, height int64) ([]*types.ParsedVote, error)
}

type CosmosParser struct {
	chainID string
}

func NewCosmosParser(chainID string) *CosmosParser {
	return &CosmosParser{
		chainID: chainID,
	}
}

// ============================================================================
// BLOCK PARSING (existing + enhanced)
// ============================================================================

func (p *CosmosParser) ParseBlock(raw *types.RawBlock) (*types.ParsedBlock, error) {
	if raw == nil {
		return nil, fmt.Errorf("raw block is nil")
	}

	parsed := &types.ParsedBlock{
		Height:       raw.Height,
		Hash:         raw.Hash,
		Time:         raw.Time,
		ProposerAddr: raw.Proposer,
		TxCount:      raw.TxCount,
		Txs:          make([]types.ParsedTx, 0, raw.TxCount),
	}

	for i, txBytes := range raw.RawTxs {
		var txResp interface{}
		if i < len(raw.TxResponses) {
			txResp = raw.TxResponses[i]
		}

		parsedTx, err := p.ParseTx(txBytes, raw.Height, int32(i), txResp)
		if err != nil {
			fmt.Printf("failed to parse tx %d in block %d: %v\n", i, raw.Height, err)
			continue
		}

		parsed.Txs = append(parsed.Txs, *parsedTx)
	}

	return parsed, nil
}

func (p *CosmosParser) ParseTx(txBytes []byte, height int64, index int32, txResp interface{}) (*types.ParsedTx, error) {
	var tx txtypes.Tx
	if err := tx.Unmarshal(txBytes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tx: %w", err)
	}

	sum := sha256.Sum256(txBytes)
	txHash := strings.ToUpper(hex.EncodeToString(sum[:]))

	parsed := &types.ParsedTx{
		Hash:      txHash,
		Height:    height,
		Index:     index,
		Messages:  make([]types.ParsedMessage, 0),
		Events:    make([]types.ParsedEvent, 0),
		MsgTypes:  make([]string, 0),
		Addresses: make([]string, 0),
	}

	if tx.AuthInfo != nil && tx.AuthInfo.Fee != nil {
		parsed.GasWanted = int64(tx.AuthInfo.Fee.GasLimit)
		if len(tx.AuthInfo.Fee.Amount) > 0 {
			parsed.Fee = tx.AuthInfo.Fee.Amount.String()
		}
	}

	if tx.Body != nil {
		parsed.Memo = tx.Body.Memo

		for _, anyMsg := range tx.Body.Messages {
			msgType := anyMsg.TypeUrl
			parsed.MsgTypes = append(parsed.MsgTypes, msgType)

			msg, err := p.parseMessage(anyMsg)
			if err != nil {
				fmt.Printf("failed to parse message %s: %v\n", msgType, err)
				continue
			}

			// parsed.Messages = append(parsed.Messages, *msg)

			// if msg.Sender != "" {
			// 	parsed.Addresses = append(parsed.Addresses, msg.Sender)
			// }
			// if msg.Receiver != "" {
			// 	parsed.Addresses = append(parsed.Addresses, msg.Receiver)
			// }
			parsed.Messages = append(parsed.Messages, *msg)

			// For MultiSend, collect ALL input/output addresses
			if strings.Contains(msg.Type, "MsgMultiSend") {
				if outputs, ok := msg.RawData["outputs"].([]map[string]interface{}); ok {
					for _, o := range outputs {
						if addr, ok := o["address"].(string); ok && addr != "" {
							parsed.Addresses = append(parsed.Addresses, addr)
						}
					}
				}
				if inputs, ok := msg.RawData["inputs"].([]map[string]interface{}); ok {
					for _, inp := range inputs {
						if addr, ok := inp["address"].(string); ok && addr != "" {
							parsed.Addresses = append(parsed.Addresses, addr)
						}
					}
				}
			}

			if msg.Sender != "" {
				parsed.Addresses = append(parsed.Addresses, msg.Sender)
			}
			if msg.Receiver != "" {
				parsed.Addresses = append(parsed.Addresses, msg.Receiver)
			}
		}
	}

	// REPLACE WITH:
	if resp, ok := txResp.(*sdk.TxResponse); ok && resp != nil {
		parsed.GasUsed = resp.GasUsed
		parsed.Code = resp.Code
		parsed.Log = resp.RawLog
		parsed.Success = resp.Code == 0

		// Bug 3 fix — parse real timestamp from tx response
		if resp.Timestamp != "" {
			t, err := time.Parse(time.RFC3339, resp.Timestamp)
			if err == nil {
				parsed.Timestamp = t
			}
		}

		for _, event := range resp.Events {
			parsed.Events = append(parsed.Events, p.parseEvent(event))
		}
	}

	parsed.Addresses = uniqueStrings(parsed.Addresses)
	return parsed, nil
}

func (p *CosmosParser) parseMessage(anyMsg *gogoany.Any) (*types.ParsedMessage, error) {
	msg := &types.ParsedMessage{
		Type:    anyMsg.TypeUrl,
		RawData: make(map[string]interface{}),
	}
	// added this for parsing the multisend transactions
	// Bank MsgMultiSend — MUST be before MsgSend check
	if strings.Contains(anyMsg.TypeUrl, "MsgMultiSend") {
		var msgMultiSend banktypes.MsgMultiSend
		if err := msgMultiSend.Unmarshal(anyMsg.Value); err != nil {
			return msg, err
		}

		inputs := make([]map[string]interface{}, 0)
		outputs := make([]map[string]interface{}, 0)

		// for _, input := range msgMultiSend.Inputs {
		// 	inputs = append(inputs, map[string]interface{}{
		// 		"address": input.Address,
		// 		"coins":   input.Coins.String(),
		// 	})
		// 	if msg.Sender == "" {
		// 		msg.Sender = input.Address
		// 	}
		// }

		// for _, output := range msgMultiSend.Outputs {
		// 	outputs = append(outputs, map[string]interface{}{
		// 		"address": output.Address,
		// 		"coins":   output.Coins.String(),
		// 	})
		// 	if msg.Receiver == "" {
		// 		msg.Receiver = output.Address
		// 	}
		// }
        for _, input := range msgMultiSend.Inputs {
			coinsList := make([]map[string]interface{}, 0)
			for _, coin := range input.Coins {
				coinsList = append(coinsList, map[string]interface{}{
					"denom":  coin.Denom,
					"amount": coin.Amount.String(),
				})
			}
			inputs = append(inputs, map[string]interface{}{
				"address": input.Address,
				"coins":   coinsList,
			})
			if msg.Sender == "" {
				msg.Sender = input.Address
			}
		}

		for _, output := range msgMultiSend.Outputs {
			coinsList := make([]map[string]interface{}, 0)
			for _, coin := range output.Coins {
				coinsList = append(coinsList, map[string]interface{}{
					"denom":  coin.Denom,
					"amount": coin.Amount.String(),
				})
			}
			outputs = append(outputs, map[string]interface{}{
				"address": output.Address,
				"coins":   coinsList,
			})
			if msg.Receiver == "" {
				msg.Receiver = output.Address
			}
		}
		msg.RawData["inputs"] = inputs
		msg.RawData["outputs"] = outputs
		return msg, nil // ← return early, don't fall through to MsgSend
	}
	// Bank MsgSend
	if strings.Contains(anyMsg.TypeUrl, "MsgSend") {
		var msgSend banktypes.MsgSend
		if err := msgSend.Unmarshal(anyMsg.Value); err != nil {
			return msg, err
		}

		msg.Sender = msgSend.FromAddress
		msg.Receiver = msgSend.ToAddress

		if len(msgSend.Amount) > 0 {
			msg.Amount = msgSend.Amount[0].Amount.String()
			msg.Denom = msgSend.Amount[0].Denom
		}

		msg.RawData["from"] = msgSend.FromAddress
		msg.RawData["to"] = msgSend.ToAddress
		msg.RawData["amount"] = msgSend.Amount.String()
	}

	// Staking MsgDelegate
	if strings.Contains(anyMsg.TypeUrl, "MsgDelegate") {
		var msgDelegate stakingtypes.MsgDelegate
		if err := msgDelegate.Unmarshal(anyMsg.Value); err != nil {
			return msg, err
		}

		msg.Sender = msgDelegate.DelegatorAddress
		msg.Receiver = msgDelegate.ValidatorAddress
		msg.Amount = msgDelegate.Amount.Amount.String()
		msg.Denom = msgDelegate.Amount.Denom

		msg.RawData["delegator"] = msgDelegate.DelegatorAddress
		msg.RawData["validator"] = msgDelegate.ValidatorAddress
		msg.RawData["amount"] = msgDelegate.Amount.String()
	}

	// Staking MsgUndelegate
	if strings.Contains(anyMsg.TypeUrl, "MsgUndelegate") {
		var msgUndelegate stakingtypes.MsgUndelegate
		if err := msgUndelegate.Unmarshal(anyMsg.Value); err != nil {
			return msg, err
		}

		msg.Sender = msgUndelegate.DelegatorAddress
		msg.Receiver = msgUndelegate.ValidatorAddress
		msg.Amount = msgUndelegate.Amount.Amount.String()
		msg.Denom = msgUndelegate.Amount.Denom

		msg.RawData["delegator"] = msgUndelegate.DelegatorAddress
		msg.RawData["validator"] = msgUndelegate.ValidatorAddress
		msg.RawData["amount"] = msgUndelegate.Amount.String()
	}

	// Gov MsgVote
	if strings.Contains(anyMsg.TypeUrl, "MsgVote") {
		var msgVote govtypes.MsgVote
		if err := msgVote.Unmarshal(anyMsg.Value); err != nil {
			return msg, err
		}

		msg.Sender = msgVote.Voter
		msg.RawData["voter"] = msgVote.Voter
		msg.RawData["proposal_id"] = msgVote.ProposalId
		msg.RawData["option"] = msgVote.Option.String()
	}

	return msg, nil
}

func (p *CosmosParser) parseEvent(event abci.Event) types.ParsedEvent {
	parsed := types.ParsedEvent{
		Type:       event.Type,
		Attributes: make(map[string]string),
	}

	for _, attr := range event.Attributes {
		parsed.Attributes[string(attr.Key)] = string(attr.Value)
	}

	return parsed
}

// ============================================================================
// VALIDATOR PARSING (NEW)
// ============================================================================

func (p *CosmosParser) ParseValidator(raw *types.RawValidator) (*types.ParsedValidator, error) {
	if raw == nil {
		return nil, fmt.Errorf("raw validator is nil")
	}

	parsed := &types.ParsedValidator{
		OperatorAddress:   raw.OperatorAddress,
		Jailed:            raw.Jailed,
		Status:            raw.Status,
		Tokens:            raw.Tokens,
		DelegatorShares:   raw.DelegatorShares,
		UnbondingHeight:   raw.UnbondingHeight,
		UnbondingTime:     raw.UnbondingTime,
		MinSelfDelegation: raw.MinSelfDelegation,
		UpdatedAt:         time.Now(),
	}

	if raw.ConsensusPubkey != nil {
		if anyPubkey, ok := raw.ConsensusPubkey.(*gogoany.Any); ok {
			parsed.ConsensusPubkey = base64.StdEncoding.EncodeToString(anyPubkey.Value)

			// Derive real consensus address from pubkey bytes
			// Last 20 bytes of pubkey value = consensus address
			pubkeyBytes := anyPubkey.Value
			if len(pubkeyBytes) >= 20 {
				// Strip protobuf prefix (first few bytes) to get raw pubkey
				rawPubkey := pubkeyBytes
				if len(pubkeyBytes) > 32 {
					rawPubkey = pubkeyBytes[len(pubkeyBytes)-32:]
				}
				// SHA256 + RIPEMD160 → take last 20 bytes
				sha := sha256.Sum256(rawPubkey)
				parsed.ConsensusAddress = hex.EncodeToString(sha[:20]) // real consensus address
			}
		}
	}

	// Parse description
	if desc, ok := raw.Description.(stakingtypes.Description); ok {
		parsed.Moniker = desc.Moniker
		parsed.Identity = desc.Identity
		parsed.Website = desc.Website
		parsed.SecurityContact = desc.SecurityContact
		parsed.Details = desc.Details
	}

	// Parse commission
	if comm, ok := raw.Commission.(stakingtypes.Commission); ok {
		parsed.CommissionRate = comm.CommissionRates.Rate.String()
		parsed.CommissionMaxRate = comm.CommissionRates.MaxRate.String()
		parsed.CommissionMaxChangeRate = comm.CommissionRates.MaxChangeRate.String()
	}

	// Calculate voting power (tokens / 1e6 for most chains)
	// This is simplified - actual calculation depends on chain config
	//parsed.VotingPower = 0 // TODO: Calculate from tokens
	if raw.Tokens != "" {
		if tokenInt, err := strconv.ParseInt(raw.Tokens, 10, 64); err == nil {
			parsed.VotingPower = tokenInt / 1_000_000
			// parsed.Power       = parsed.VotingPower
		}
	}
	return parsed, nil
}

func (p *CosmosParser) ParseValidators(raws []*types.RawValidator) ([]*types.ParsedValidator, error) {
	parsed := make([]*types.ParsedValidator, 0, len(raws))

	for _, raw := range raws {
		val, err := p.ParseValidator(raw)
		if err != nil {
			fmt.Printf("failed to parse validator: %v\n", err)
			continue
		}
		parsed = append(parsed, val)
	}

	return parsed, nil
}

// ============================================================================
// DELEGATION PARSING (NEW)
// ============================================================================

func (p *CosmosParser) ParseDelegation(raw *types.RawDelegation, height int64) (*types.ParsedDelegation, error) {
	if raw == nil {
		return nil, fmt.Errorf("raw delegation is nil")
	}

	parsed := &types.ParsedDelegation{
		DelegatorAddress: raw.DelegatorAddress,
		ValidatorAddress: raw.ValidatorAddress,
		Shares:           raw.Shares,
		Amount:           "0", // Calculate from shares
		Denom:            "stake",
		Height:           height,
		UpdatedAt:        time.Now(),
	}

	return parsed, nil
}

func (p *CosmosParser) ParseDelegations(raws []*types.RawDelegation, height int64) ([]*types.ParsedDelegation, error) {
	parsed := make([]*types.ParsedDelegation, 0, len(raws))

	for _, raw := range raws {
		del, err := p.ParseDelegation(raw, height)
		if err != nil {
			fmt.Printf("failed to parse delegation: %v\n", err)
			continue
		}
		parsed = append(parsed, del)
	}

	return parsed, nil
}

// ============================================================================
// BALANCE PARSING (NEW)
// ============================================================================

func (p *CosmosParser) ParseBalance(raw *types.RawBalance, height int64) (*types.ParsedBalance, error) {
	if raw == nil {
		return nil, fmt.Errorf("raw balance is nil")
	}

	parsed := &types.ParsedBalance{
		Address:   raw.Address,
		Denom:     raw.Denom,
		Amount:    raw.Amount,
		Height:    height,
		UpdatedAt: time.Now(),
	}

	return parsed, nil
}

func (p *CosmosParser) ParseBalances(raws []*types.RawBalance, height int64) ([]*types.ParsedBalance, error) {
	parsed := make([]*types.ParsedBalance, 0, len(raws))

	for _, raw := range raws {
		bal, err := p.ParseBalance(raw, height)
		if err != nil {
			fmt.Printf("failed to parse balance: %v\n", err)
			continue
		}
		parsed = append(parsed, bal)
	}

	return parsed, nil
}

// ============================================================================
// PROPOSAL PARSING (NEW)
// ============================================================================

func (p *CosmosParser) ParseProposal(raw *types.RawProposal, height int64) (*types.ParsedProposal, error) {
	if raw == nil {
		return nil, fmt.Errorf("raw proposal is nil")
	}

	parsed := &types.ParsedProposal{
		ProposalID: raw.ProposalID,

		Title:        raw.Title,
		Description:  raw.Summary,
		Metadata:     raw.Metadata,
		Proposer:     raw.Proposer,
		ProposalType: "gov.v1",

		Status:          raw.Status,
		SubmitTime:      raw.SubmitTime,
		DepositEndTime:  raw.DepositEndTime,
		VotingStartTime: raw.VotingStartTime,
		VotingEndTime:   raw.VotingEndTime,

		Height:    height,
		UpdatedAt: time.Now(),
	}

	// TOTAL DEPOSIT
	if len(raw.TotalDeposit) > 0 {
		//parsed.TotalDeposit = raw.TotalDeposit[0].Amount
		parsed.TotalDeposit = raw.TotalDeposit[0].Amount.String()
		parsed.DepositDenom = raw.TotalDeposit[0].Denom

		//parsed.DepositDenom = raw.TotalDeposit[0].Denom
	}

	// FINAL TALLY
	if raw.FinalTallyResult != nil {
		parsed.YesVotes = raw.FinalTallyResult.YesCount
		parsed.NoVotes = raw.FinalTallyResult.NoCount
		parsed.AbstainVotes = raw.FinalTallyResult.AbstainCount
		parsed.NoWithVetoVotes = raw.FinalTallyResult.NoWithVetoCount
	}

	// MESSAGES → JSON
	if len(raw.Messages) > 0 {
		msgTypes := make([]string, len(raw.Messages))

		for i, msg := range raw.Messages {
			msgTypes[i] = msg.TypeUrl
		}

		bz, _ := json.Marshal(msgTypes)
		parsed.Messages = string(bz)

		parsed.ProposalType = msgTypes[0]
	}

	return parsed, nil
}

func (p *CosmosParser) ParseProposals(raws []*types.RawProposal, height int64) ([]*types.ParsedProposal, error) {
	parsed := make([]*types.ParsedProposal, 0, len(raws))

	for _, raw := range raws {
		prop, err := p.ParseProposal(raw, height)
		if err != nil {
			fmt.Printf("failed to parse proposal: %v\n", err)
			continue
		}
		parsed = append(parsed, prop)
	}

	return parsed, nil
}

// ============================================================================
// VOTE PARSING (NEW)
// ============================================================================

func (p *CosmosParser) ParseVote(raw *types.RawVote, height int64, txHash *string, timestamp time.Time) (*types.ParsedVote, error) {
	if raw == nil {
		return nil, fmt.Errorf("raw vote is nil")
	}

	parsed := &types.ParsedVote{
		ProposalID: raw.ProposalID,
		Voter:      raw.Voter,
		Option:     raw.Option,
		Height:     height,
		TxHash:     txHash,
		Timestamp:  timestamp,
	}

	// Parse weighted vote options if present
	if len(raw.Options) > 0 {
		parsed.Options = make([]types.VoteOption, 0, len(raw.Options))
	}

	return parsed, nil
}

func (p *CosmosParser) ParseVotes(raws []*types.RawVote, height int64) ([]*types.ParsedVote, error) {
	parsed := make([]*types.ParsedVote, 0, len(raws))

	for _, raw := range raws {
		vote, err := p.ParseVote(raw, height, nil, time.Now())
		if err != nil {
			fmt.Printf("failed to parse vote: %v\n", err)
			continue
		}
		parsed = append(parsed, vote)
	}

	return parsed, nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

func uniqueStrings(input []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)

	for _, str := range input {
		if !seen[str] {
			seen[str] = true
			result = append(result, str)
		}
	}
	return result
}
