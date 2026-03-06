package fetcher

import (
	"context"
	// "crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/cosmos-indexer/pkg/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	tmservice "cosmossdk.io/api/cosmos/base/tendermint/v1beta1"
	querytypes "github.com/cosmos/cosmos-sdk/types/query"
	txtypes "github.com/cosmos/cosmos-sdk/types/tx"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/gogoproto/proto"
)

//
// ============================================================================
// Fetcher Interface
// ============================================================================
//
// Fetcher defines the data access layer for chain queries.
//
// It abstracts all blockchain RPC/gRPC calls so the coordinator
// remains independent from transport details.
//
// All methods return raw, minimally transformed models that are later
// parsed into structured domain entities.

type Fetcher interface {

	// --- Block operations ---
	GetLatestHeight(ctx context.Context) (int64, error)
	FetchBlock(ctx context.Context, height int64) (*types.RawBlock, error)
	FetchTx(ctx context.Context, hash string) (*types.RawTx, error)

	// --- Validator operations ---
	FetchValidators(ctx context.Context) ([]*types.RawValidator, error)
	FetchValidator(ctx context.Context, validatorAddr string) (*types.RawValidator, error)

	// --- Delegation operations ---
	FetchDelegations(ctx context.Context, validatorAddr string) ([]*types.RawDelegation, error)
	FetchDelegationsByDelegator(ctx context.Context, delegatorAddr string) ([]*types.RawDelegation, error)

	// --- Balance operations ---
	FetchBalances(ctx context.Context, address string) ([]*types.RawBalance, error)
	FetchBalance(ctx context.Context, address, denom string) (*types.RawBalance, error)

	// --- Governance operations ---
	FetchProposals(ctx context.Context) ([]*types.RawProposal, error)
	FetchProposal(ctx context.Context, proposalID uint64) (*types.RawProposal, error)
	FetchVotes(ctx context.Context, proposalID uint64) ([]*types.RawVote, error)

	Close() error
}

//
// ============================================================================
// GRPCFetcher Implementation
// ============================================================================
//
// GRPCFetcher implements Fetcher using Cosmos SDK gRPC services.
//
// It maintains a single persistent gRPC connection and initializes
// module-specific query clients.
//
// This design avoids repeated connection setup and allows efficient
// high-throughput block indexing.
//

type GRPCFetcher struct {
	conn          *grpc.ClientConn
	tmClient      tmservice.ServiceClient
	txClient      txtypes.ServiceClient
	stakingClient stakingtypes.QueryClient
	bankClient    banktypes.QueryClient
	govClient     govtypes.QueryClient
	grpcEndpoint  string
}

// NewGRPCFetcher establishes a blocking gRPC connection with timeout.
func NewGRPCFetcher(grpcEndpoint string) (*GRPCFetcher, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		grpcEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), // block until connection established
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC endpoint %s: %w", grpcEndpoint, err)
	}

	return &GRPCFetcher{
		conn:          conn,
		tmClient:      tmservice.NewServiceClient(conn),
		txClient:      txtypes.NewServiceClient(conn),
		stakingClient: stakingtypes.NewQueryClient(conn),
		bankClient:    banktypes.NewQueryClient(conn),
		govClient:     govtypes.NewQueryClient(conn),
		grpcEndpoint:  grpcEndpoint,
	}, nil
}

//
// ============================================================================
// BLOCK OPERATIONS
// ============================================================================
//
// These methods query Tendermint and Tx services.
//

// GetLatestHeight retrieves the latest block height from the chain.
func (f *GRPCFetcher) GetLatestHeight(ctx context.Context) (int64, error) {
	resp, err := f.tmClient.GetLatestBlock(ctx, &tmservice.GetLatestBlockRequest{})
	if err != nil {
		return 0, err
	}

	if resp.Block == nil {
		return 0, fmt.Errorf("nil block")
	}

	return resp.Block.Header.Height, nil
}

// FetchBlock retrieves a block by height and converts it into RawBlock.
// Only minimal transformation is performed.
func (f *GRPCFetcher) FetchBlock(ctx context.Context, height int64) (*types.RawBlock, error) {
	resp, err := f.tmClient.GetBlockByHeight(ctx, &tmservice.GetBlockByHeightRequest{
		Height: height,
	})
	if err != nil {
		return nil, err
	}

	if resp.Block == nil {
		return nil, fmt.Errorf("block not found")
	}

	block := resp.Block
	header := block.Header

	// Collect raw transaction bytes
	txBytes := make([][]byte, 0, len(block.Data.Txs))
	for _, tx := range block.Data.Txs {
		txBytes = append(txBytes, tx)
	}

	rawBlock := &types.RawBlock{
		Height:    header.Height,
		Hash:      hex.EncodeToString(resp.BlockId.Hash),
		Time:      header.Time.AsTime(),
		Proposer:  hex.EncodeToString(header.ProposerAddress),
		TxCount:   int32(len(txBytes)),
		RawTxs:    txBytes,
		BlockData: resp,
	}

	return rawBlock, nil
}

// FetchTx retrieves a transaction by hash using the Tx gRPC service.
// The transaction bytes are re-marshaled to compute canonical SHA256 hash.
func (f *GRPCFetcher) FetchTx(ctx context.Context, hash string) (*types.RawTx, error) {
	resp, err := f.txClient.GetTx(ctx, &txtypes.GetTxRequest{
		Hash: hash,
	})
	if err != nil {
		return nil, err
	}

	if resp.Tx == nil || resp.TxResponse == nil {
		return nil, fmt.Errorf("invalid tx response")
	}

	txBytes, err := proto.Marshal(resp.Tx)
	if err != nil {
		return nil, err
	}

	// sum := sha256.Sum256(txBytes)
	// txHash := hex.EncodeToString(sum[:])

	rawTx := &types.RawTx{
		Hash:       resp.TxResponse.TxHash,
		RawBytes:   txBytes,
		Height:     resp.TxResponse.Height,
		TxResponse: resp.TxResponse,
		Index:      0,
	}

	return rawTx, nil
}

//
// ============================================================================
// PAGINATED MODULE QUERIES
// ============================================================================
//
// Many Cosmos SDK queries use pagination via PageRequest.
//
// All paginated queries in this file follow the same pattern:
//   - Loop until NextKey is empty
//   - Accumulate results
//   - Return full dataset
//
// This ensures consistent full-state sync behavior.
//
//
// ============================================================================
// VALIDATOR OPERATIONS
// ============================================================================
//
// FetchValidators retrieves the full validator set using pagination.
// This method is typically used for periodic state snapshots.
//

func (f *GRPCFetcher) FetchValidators(ctx context.Context) ([]*types.RawValidator, error) {
	var allValidators []*types.RawValidator
	var nextKey []byte

	for {
		resp, err := f.stakingClient.Validators(ctx, &stakingtypes.QueryValidatorsRequest{
			Pagination: &querytypes.PageRequest{
				Key:   nextKey,
				Limit: 100,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to fetch validators: %w", err)
		}

		for _, val := range resp.Validators {
			rawVal := &types.RawValidator{
				OperatorAddress:   val.OperatorAddress,
				ConsensusPubkey:   val.ConsensusPubkey,
				Jailed:            val.Jailed,
				Status:            val.Status.String(),
				Tokens:            val.Tokens.String(),
				DelegatorShares:   val.DelegatorShares.String(),
				Description:       val.Description,
				UnbondingHeight:   val.UnbondingHeight,
				UnbondingTime:     val.UnbondingTime,
				Commission:        val.Commission,
				MinSelfDelegation: val.MinSelfDelegation.String(),
			}
			allValidators = append(allValidators, rawVal)
		}

		if resp.Pagination == nil || len(resp.Pagination.NextKey) == 0 {
			break
		}
		nextKey = resp.Pagination.NextKey
	}

	return allValidators, nil
}

func (f *GRPCFetcher) FetchValidator(ctx context.Context, validatorAddr string) (*types.RawValidator, error) {
	resp, err := f.stakingClient.Validator(ctx, &stakingtypes.QueryValidatorRequest{
		ValidatorAddr: validatorAddr,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch validator %s: %w", validatorAddr, err)
	}

	val := resp.Validator
	rawVal := &types.RawValidator{
		OperatorAddress:   val.OperatorAddress,
		ConsensusPubkey:   val.ConsensusPubkey,
		Jailed:            val.Jailed,
		Status:            val.Status.String(),
		Tokens:            val.Tokens.String(),
		DelegatorShares:   val.DelegatorShares.String(),
		Description:       val.Description,
		UnbondingHeight:   val.UnbondingHeight,
		UnbondingTime:     val.UnbondingTime,
		Commission:        val.Commission,
		MinSelfDelegation: val.MinSelfDelegation.String(),
	}

	return rawVal, nil
}

//
// ============================================================================
// DELEGATION OPERATIONS
// ============================================================================
//
// FetchDelegationsByDelegator retrieves all delegations for a delegator.
// Useful after MsgDelegate events to refresh delegator state.
//

func (f *GRPCFetcher) FetchDelegations(ctx context.Context, validatorAddr string) ([]*types.RawDelegation, error) {
	var allDelegations []*types.RawDelegation
	var nextKey []byte

	for {
		resp, err := f.stakingClient.ValidatorDelegations(ctx, &stakingtypes.QueryValidatorDelegationsRequest{
			ValidatorAddr: validatorAddr,
			Pagination: &querytypes.PageRequest{
				Key:   nextKey,
				Limit: 100,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to fetch delegations for validator %s: %w", validatorAddr, err)
		}

		for _, del := range resp.DelegationResponses {
			rawDel := &types.RawDelegation{
				DelegatorAddress: del.Delegation.DelegatorAddress,
				ValidatorAddress: del.Delegation.ValidatorAddress,
				Shares:           del.Delegation.Shares.String(),
			}
			allDelegations = append(allDelegations, rawDel)
		}

		if resp.Pagination == nil || len(resp.Pagination.NextKey) == 0 {
			break
		}
		nextKey = resp.Pagination.NextKey
	}

	return allDelegations, nil
}

func (f *GRPCFetcher) FetchDelegationsByDelegator(ctx context.Context, delegatorAddr string) ([]*types.RawDelegation, error) {
	var allDelegations []*types.RawDelegation
	var nextKey []byte

	for {
		resp, err := f.stakingClient.DelegatorDelegations(ctx, &stakingtypes.QueryDelegatorDelegationsRequest{
			DelegatorAddr: delegatorAddr,
			Pagination: &querytypes.PageRequest{
				Key:   nextKey,
				Limit: 100,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to fetch delegations for delegator %s: %w", delegatorAddr, err)
		}

		for _, del := range resp.DelegationResponses {
			rawDel := &types.RawDelegation{
				DelegatorAddress: del.Delegation.DelegatorAddress,
				ValidatorAddress: del.Delegation.ValidatorAddress,
				Shares:           del.Delegation.Shares.String(),
			}
			allDelegations = append(allDelegations, rawDel)
		}

		if resp.Pagination == nil || len(resp.Pagination.NextKey) == 0 {
			break
		}
		nextKey = resp.Pagination.NextKey
	}

	return allDelegations, nil
}

//
// ============================================================================
// BALANCE OPERATIONS
// ============================================================================
//
// FetchBalances retrieves all token balances for an account.
// Used to refresh account state after MsgSend events.
//

func (f *GRPCFetcher) FetchBalances(ctx context.Context, address string) ([]*types.RawBalance, error) {
	var allBalances []*types.RawBalance
	var nextKey []byte

	for {
		resp, err := f.bankClient.AllBalances(ctx, &banktypes.QueryAllBalancesRequest{
			Address: address,
			Pagination: &querytypes.PageRequest{
				Key:   nextKey,
				Limit: 100,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to fetch balances for %s: %w", address, err)
		}

		for _, coin := range resp.Balances {
			rawBal := &types.RawBalance{
				Address: address,
				Denom:   coin.Denom,
				Amount:  coin.Amount.String(),
			}
			allBalances = append(allBalances, rawBal)
		}

		if resp.Pagination == nil || len(resp.Pagination.NextKey) == 0 {
			break
		}
		nextKey = resp.Pagination.NextKey
	}

	return allBalances, nil
}

func (f *GRPCFetcher) FetchBalance(ctx context.Context, address, denom string) (*types.RawBalance, error) {
	resp, err := f.bankClient.Balance(ctx, &banktypes.QueryBalanceRequest{
		Address: address,
		Denom:   denom,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch balance for %s/%s: %w", address, denom, err)
	}

	rawBal := &types.RawBalance{
		Address: address,
		Denom:   resp.Balance.Denom,
		Amount:  resp.Balance.Amount.String(),
	}

	return rawBal, nil
}

//
// ============================================================================
// GOVERNANCE OPERATIONS
// ============================================================================
//
// Governance queries retrieve proposals and votes.
// These are typically synced periodically rather than per-transaction.
//

func (f *GRPCFetcher) FetchProposals(ctx context.Context) ([]*types.RawProposal, error) {
	var allProposals []*types.RawProposal
	var nextKey []byte

	for {
		resp, err := f.govClient.Proposals(ctx, &govtypes.QueryProposalsRequest{
			Pagination: &querytypes.PageRequest{
				Key:   nextKey,
				Limit: 100,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to fetch proposals: %w", err)
		}

		for _, prop := range resp.Proposals {
			// Convert TotalDeposit to interface slice
			totalDeposit := make([]interface{}, len(prop.TotalDeposit))
			for i, coin := range prop.TotalDeposit {
				totalDeposit[i] = coin
			}

			rawProp := &types.RawProposal{
				ProposalID:       prop.Id,
				Title:            prop.Title,
				Summary:          prop.Summary,
				Metadata:         prop.Metadata,
				Proposer:         prop.Proposer,
				Messages:         prop.Messages,
				Status:           prop.Status.String(),
				FinalTallyResult: prop.FinalTallyResult,
				SubmitTime:       *prop.SubmitTime,
				DepositEndTime:   *prop.DepositEndTime,
				TotalDeposit:     prop.TotalDeposit,
				VotingStartTime:  *prop.VotingStartTime,
				VotingEndTime:    *prop.VotingEndTime,
			}

			allProposals = append(allProposals, rawProp)
		}

		if resp.Pagination == nil || len(resp.Pagination.NextKey) == 0 {
			break
		}
		nextKey = resp.Pagination.NextKey
	}

	return allProposals, nil
}

func (f *GRPCFetcher) FetchProposal(ctx context.Context, proposalID uint64) (*types.RawProposal, error) {
	resp, err := f.govClient.Proposal(ctx, &govtypes.QueryProposalRequest{
		ProposalId: proposalID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch proposal %d: %w", proposalID, err)
	}

	prop := resp.Proposal

	rawProp := &types.RawProposal{
		ProposalID:       prop.Id,
		Title:            prop.Title,
		Summary:          prop.Summary,
		Metadata:         prop.Metadata,
		Proposer:         prop.Proposer,
		Messages:         prop.Messages,
		Status:           prop.Status.String(),
		FinalTallyResult: prop.FinalTallyResult,
		SubmitTime:       *prop.SubmitTime,
		DepositEndTime:   *prop.DepositEndTime,
		TotalDeposit:     prop.TotalDeposit,
		VotingStartTime:  *prop.VotingStartTime,
		VotingEndTime:    *prop.VotingEndTime,
	}

	return rawProp, nil
}

func (f *GRPCFetcher) FetchVotes(ctx context.Context, proposalID uint64) ([]*types.RawVote, error) {
	var allVotes []*types.RawVote
	var nextKey []byte

	for {
		resp, err := f.govClient.Votes(ctx, &govtypes.QueryVotesRequest{
			ProposalId: proposalID,
			Pagination: &querytypes.PageRequest{
				Key:   nextKey,
				Limit: 100,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to fetch votes for proposal %d: %w", proposalID, err)
		}

		for _, vote := range resp.Votes {
			// Convert Options to interface slice
			options := make([]interface{}, len(vote.Options))
			for i, opt := range vote.Options {
				options[i] = opt
			}

			option := ""
			if len(vote.Options) > 0 {
				option = vote.Options[0].Option.String()
			}

			rawVote := &types.RawVote{
				ProposalID: vote.ProposalId,
				Voter:      vote.Voter,
				Option:     option, //vote.Options[0].Option.String(),
				Options:    options,
			}
			allVotes = append(allVotes, rawVote)
		}

		if resp.Pagination == nil || len(resp.Pagination.NextKey) == 0 {
			break
		}
		nextKey = resp.Pagination.NextKey
	}

	return allVotes, nil
}

// Close cleanly shuts down the gRPC connection.
func (f *GRPCFetcher) Close() error {
	if f.conn != nil {
		return f.conn.Close()
	}
	return nil
}
