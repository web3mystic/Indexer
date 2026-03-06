package types

import (
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types/v1"

	//"google.golang.org/protobuf/types/known/anypb"
	gogoany "github.com/cosmos/gogoproto/types/any"
)

// ============================================================================
// RAW DATA TYPES (from gRPC)
// ============================================================================

// RawBlock represents raw blockchain data from gRPC
type RawBlock struct {
	Height      int64
	Hash        string
	Time        time.Time
	Proposer    string
	TxCount     int32
	RawTxs      [][]byte
	BlockData   interface{} // For additional block metadata
	TxResponses []interface{}
}

// RawTx represents raw transaction data from gRPC
type RawTx struct {
	Hash       string
	RawBytes   []byte
	Height     int64
	TxResponse interface{} // Full tx response from chain
	Index      int32
}

// RawValidator represents raw validator data from gRPC
type RawValidator struct {
	OperatorAddress   string
	ConsensusPubkey   interface{}
	Jailed            bool
	Status            string
	Tokens            string
	DelegatorShares   string
	Description       interface{}
	UnbondingHeight   int64
	UnbondingTime     time.Time
	Commission        interface{}
	MinSelfDelegation string
}

// RawDelegation represents raw delegation data from gRPC
type RawDelegation struct {
	DelegatorAddress string
	ValidatorAddress string
	Shares           string
}

// RawBalance represents raw balance data from gRPC
type RawBalance struct {
	Address string
	Denom   string
	Amount  string
}

// RawProposal represents raw proposal data from gRPC
type RawProposal struct {
	ProposalID uint64
	Title      string
	Summary    string
	Metadata   string
	Proposer   string
	//Messages       []*anypb.Any
	//Messages        []*any.Any
	Messages         []*gogoany.Any
	Status           string
	FinalTallyResult *govtypes.TallyResult
	SubmitTime       time.Time
	DepositEndTime   time.Time
	TotalDeposit     sdk.Coins
	VotingStartTime  time.Time
	VotingEndTime    time.Time
}

// RawVote represents raw vote data from gRPC
type RawVote struct {
	ProposalID uint64
	Voter      string
	Option     string
	Options    []interface{}
}

// ============================================================================
// PARSED DATA TYPES (ready for storage)
// ============================================================================

// ParsedBlock represents structured block data ready for storage
type ParsedBlock struct {
	Height        int64
	Hash          string
	Time          time.Time
	ProposerAddr  string
	TxCount       int32
	Txs           []ParsedTx
	ValidatorSigs int32
}

// ParsedTx represents structured transaction data
type ParsedTx struct {
	Hash      string
	Height    int64
	Index     int32
	GasUsed   int64
	GasWanted int64
	Fee       string
	Success   bool
	Code      uint32
	Log       string
	Memo      string
	MsgTypes  []string
	Addresses []string // All addresses involved
	Messages  []ParsedMessage
	Events    []ParsedEvent
	Timestamp time.Time

	WasmEvents []*WasmEvent
	SDKEvents  []ParsedEvent
}

// ParsedMessage represents a decoded message
type ParsedMessage struct {
	Type     string
	Sender   string
	Receiver string
	Amount   string
	Denom    string
	RawData  map[string]interface{}

	RawBytes []byte
}

// ParsedEvent represents a blockchain event
type ParsedEvent struct {
	Type       string
	Attributes map[string]string
}

// ParsedValidator represents structured validator data
type ParsedValidator struct {
	OperatorAddress         string
	ConsensusAddress        string
	ConsensusPubkey         string
	Jailed                  bool
	Status                  string // BOND_STATUS_UNBONDED, BOND_STATUS_UNBONDING, BOND_STATUS_BONDED
	Tokens                  string
	DelegatorShares         string
	Moniker                 string
	Identity                string
	Website                 string
	SecurityContact         string
	Details                 string
	UnbondingHeight         int64
	UnbondingTime           time.Time
	CommissionRate          string
	CommissionMaxRate       string
	CommissionMaxChangeRate string
	MinSelfDelegation       string
	VotingPower             int64
	UpdatedAt               time.Time
}

// ParsedDelegation represents structured delegation data
type ParsedDelegation struct {
	DelegatorAddress string
	ValidatorAddress string
	Shares           string
	Amount           string
	Denom            string
	Height           int64
	UpdatedAt        time.Time
}

// ParsedBalance represents structured balance data
type ParsedBalance struct {
	Address   string
	Denom     string
	Amount    string
	Height    int64
	UpdatedAt time.Time
}

// ParsedProposal represents structured proposal data
type ParsedProposal struct {
	ProposalID      uint64
	Title           string
	Description     string
	ProposalType    string
	Status          string // PROPOSAL_STATUS_*
	SubmitTime      time.Time
	DepositEndTime  time.Time
	VotingStartTime time.Time
	VotingEndTime   time.Time
	TotalDeposit    string
	DepositDenom    string
	Metadata        string
	Messages        string
	YesVotes        string
	NoVotes         string
	AbstainVotes    string
	NoWithVetoVotes string
	Proposer        string
	Height          int64
	UpdatedAt       time.Time
}

// ParsedVote represents structured vote data
type ParsedVote struct {
	ProposalID uint64
	Voter      string
	Option     string
	Options    []VoteOption // For weighted votes
	Height     int64
	TxHash     *string
	Timestamp  time.Time
}

// VoteOption represents a single vote option (for weighted voting)
type VoteOption struct {
	Option string
	Weight string
}

// ParsedUnbonding represents unbonding delegation data
type ParsedUnbonding struct {
	DelegatorAddress string
	ValidatorAddress string
	Entries          []UnbondingEntry
	Height           int64
	UpdatedAt        time.Time
}

// UnbondingEntry represents a single unbonding entry
type UnbondingEntry struct {
	CreationHeight int64
	CompletionTime time.Time
	InitialBalance string
	Balance        string
}

// ParsedRedelegation represents redelegation data
type ParsedRedelegation struct {
	DelegatorAddress    string
	ValidatorSrcAddress string
	ValidatorDstAddress string
	Entries             []RedelegationEntry
	Height              int64
	UpdatedAt           time.Time
}

// RedelegationEntry represents a single redelegation entry
type RedelegationEntry struct {
	CreationHeight int64
	CompletionTime time.Time
	InitialBalance string
	SharesDst      string
}

// ============================================================================
// INDEXER STATE & PROGRESS
// ============================================================================

// IndexerState tracks indexing progress
type IndexerState struct {
	LastHeight    int64
	LastBlockHash string
	UpdatedAt     time.Time
}

// ValidatorSyncState tracks validator sync progress
type ValidatorSyncState struct {
	LastSyncHeight  int64
	LastSyncTime    time.Time
	TotalValidators int
}

// ProposalSyncState tracks proposal sync progress
type ProposalSyncState struct {
	LastSyncHeight  int64
	LastSyncTime    time.Time
	ActiveProposals int
}

// ============================================================================
// STATISTICS & AGGREGATIONS
// ============================================================================

// ValidatorStats represents validator statistics
type ValidatorStats struct {
	OperatorAddress  string
	TotalDelegations int64
	TotalDelegators  int64
	SelfDelegation   string
	Uptime           float64
	BlocksProposed   int64
	BlocksMissed     int64
	LastActiveHeight int64
}

// ProposalStats represents proposal statistics
type ProposalStats struct {
	ProposalID     uint64
	TotalVotes     int64
	TotalDeposits  int64
	VotingPower    string
	Turnout        float64
	PassPercentage float64
}

// ============================================================================
// HELPER TYPES
// ============================================================================

// AddressType identifies the type of address
type AddressType string

const (
	AddressTypeValidator AddressType = "validator"
	AddressTypeAccount   AddressType = "account"
	AddressTypeContract  AddressType = "contract"
)

// TransactionType identifies the type of transaction
type TransactionType string

const (
	TxTypeTransfer   TransactionType = "transfer"
	TxTypeDelegate   TransactionType = "delegate"
	TxTypeUndelegate TransactionType = "undelegate"
	TxTypeRedelegate TransactionType = "redelegate"
	TxTypeVote       TransactionType = "vote"
	TxTypeProposal   TransactionType = "proposal"
	TxTypeOther      TransactionType = "other"
)

// BondStatus represents validator bond status
type BondStatus string

const (
	BondStatusUnbonded  BondStatus = "BOND_STATUS_UNBONDED"
	BondStatusUnbonding BondStatus = "BOND_STATUS_UNBONDING"
	BondStatusBonded    BondStatus = "BOND_STATUS_BONDED"
)

// ProposalStatus represents proposal status
type ProposalStatus string

const (
	ProposalStatusUnspecified   ProposalStatus = "PROPOSAL_STATUS_UNSPECIFIED"
	ProposalStatusDepositPeriod ProposalStatus = "PROPOSAL_STATUS_DEPOSIT_PERIOD"
	ProposalStatusVotingPeriod  ProposalStatus = "PROPOSAL_STATUS_VOTING_PERIOD"
	ProposalStatusPassed        ProposalStatus = "PROPOSAL_STATUS_PASSED"
	ProposalStatusRejected      ProposalStatus = "PROPOSAL_STATUS_REJECTED"
	ProposalStatusFailed        ProposalStatus = "PROPOSAL_STATUS_FAILED"
)

// VoteOption represents vote option
type VoteOptionType string

const (
	VoteOptionYes        VoteOptionType = "VOTE_OPTION_YES"
	VoteOptionAbstain    VoteOptionType = "VOTE_OPTION_ABSTAIN"
	VoteOptionNo         VoteOptionType = "VOTE_OPTION_NO"
	VoteOptionNoWithVeto VoteOptionType = "VOTE_OPTION_NO_WITH_VETO"
)

// =============================================================================
// COSMWASM TYPES
// =============================================================================

// WasmContract represents a deployed CosmWasm contract
// Maps to: wasm_contracts table
type WasmContract struct {
	ContractAddress      string                 `db:"contract_address" json:"contract_address"`
	CodeID               int64                  `db:"code_id" json:"code_id"`
	Creator              string                 `db:"creator" json:"creator"`
	Admin                string                 `db:"admin" json:"admin"`
	Label                string                 `db:"label" json:"label"`
	InitMsg              map[string]interface{} `db:"init_msg" json:"init_msg"`
	ContractInfo         map[string]interface{} `db:"contract_info" json:"contract_info"`
	InstantiatedAtHeight int64                  `db:"instantiated_at_height" json:"instantiated_at_height"`
	InstantiatedAtTime   time.Time              `db:"instantiated_at_time" json:"instantiated_at_time"`
	InstantiateTxHash    string                 `db:"instantiate_tx_hash" json:"instantiate_tx_hash"`
	CurrentCodeID        int64                  `db:"current_code_id" json:"current_code_id"`
	LastMigratedHeight   int64                  `db:"last_migrated_height" json:"last_migrated_height"`
	LastMigratedTxHash   string                 `db:"last_migrated_tx_hash" json:"last_migrated_tx_hash"`
	IsActive             bool                   `db:"is_active" json:"is_active"`
	UpdatedAt            time.Time              `db:"updated_at" json:"updated_at"`
	CreatedAt            time.Time              `db:"created_at" json:"created_at"`
}

// WasmCode represents an uploaded code/binary
// Maps to: wasm_codes table
type WasmCode struct {
	CodeID         int64     `db:"code_id" json:"code_id"`
	Creator        string    `db:"creator" json:"creator"`
	Checksum       string    `db:"checksum" json:"checksum"`
	Permission     string    `db:"permission" json:"permission"`
	UploadedHeight int64     `db:"uploaded_height" json:"uploaded_height"`
	UploadedTime   time.Time `db:"uploaded_time" json:"uploaded_time"`
	UploadTxHash   string    `db:"upload_tx_hash" json:"upload_tx_hash"`
	CreatedAt      time.Time `db:"created_at" json:"created_at"`
}

// WasmExecution represents a MsgExecuteContract call
// Maps to: wasm_executions table
type WasmExecution struct {
	ID              int64                  `db:"id" json:"id"`
	TxHash          string                 `db:"tx_hash" json:"tx_hash"`
	MsgIndex        int                    `db:"msg_index" json:"msg_index"`
	Height          int64                  `db:"height" json:"height"`
	Sender          string                 `db:"sender" json:"sender"`
	ContractAddress string                 `db:"contract_address" json:"contract_address"`
	ExecuteMsg      map[string]interface{} `db:"execute_msg" json:"execute_msg"`
	ExecuteAction   string                 `db:"execute_action" json:"execute_action"` // top-level key
	Funds           []WasmCoin             `db:"funds" json:"funds"`
	GasUsed         int64                  `db:"gas_used" json:"gas_used"`
	Success         bool                   `db:"success" json:"success"`
	Error           string                 `db:"error" json:"error"`
	Timestamp       time.Time              `db:"timestamp" json:"timestamp"`
}

// WasmInstantiation represents a MsgInstantiateContract call
// Maps to: wasm_instantiations table
type WasmInstantiation struct {
	ID              int64                  `db:"id" json:"id"`
	TxHash          string                 `db:"tx_hash" json:"tx_hash"`
	MsgIndex        int                    `db:"msg_index" json:"msg_index"`
	Height          int64                  `db:"height" json:"height"`
	Creator         string                 `db:"creator" json:"creator"`
	Admin           string                 `db:"admin" json:"admin"`
	CodeID          int64                  `db:"code_id" json:"code_id"`
	Label           string                 `db:"label" json:"label"`
	ContractAddress string                 `db:"contract_address" json:"contract_address"` // from event
	InitMsg         map[string]interface{} `db:"init_msg" json:"init_msg"`
	Funds           []WasmCoin             `db:"funds" json:"funds"`
	Success         bool                   `db:"success" json:"success"`
	Error           string                 `db:"error" json:"error"`
	Timestamp       time.Time              `db:"timestamp" json:"timestamp"`
}

// WasmMigration represents a MsgMigrateContract call
// Maps to: wasm_migrations table
type WasmMigration struct {
	ID              int64                  `db:"id" json:"id"`
	TxHash          string                 `db:"tx_hash" json:"tx_hash"`
	MsgIndex        int                    `db:"msg_index" json:"msg_index"`
	Height          int64                  `db:"height" json:"height"`
	Sender          string                 `db:"sender" json:"sender"`
	ContractAddress string                 `db:"contract_address" json:"contract_address"`
	OldCodeID       int64                  `db:"old_code_id" json:"old_code_id"`
	NewCodeID       int64                  `db:"new_code_id" json:"new_code_id"`
	MigrateMsg      map[string]interface{} `db:"migrate_msg" json:"migrate_msg"`
	Success         bool                   `db:"success" json:"success"`
	Error           string                 `db:"error" json:"error"`
	Timestamp       time.Time              `db:"timestamp" json:"timestamp"`
}

// WasmEvent represents a structured "wasm" type event
// Maps to: wasm_events table
type WasmEvent struct {
	ID              int64             `db:"id" json:"id"`
	TxHash          string            `db:"tx_hash" json:"tx_hash"`
	MsgIndex        int               `db:"msg_index" json:"msg_index"`
	EventIndex      int               `db:"event_index" json:"event_index"`
	Height          int64             `db:"height" json:"height"`
	ContractAddress string            `db:"contract_address" json:"contract_address"`
	Action          string            `db:"action" json:"action"`
	RawAttributes   map[string]string `db:"raw_attributes" json:"raw_attributes"`
	Timestamp       time.Time         `db:"timestamp" json:"timestamp"`
}

// CW20Transfer represents a CW20 token transfer parsed from wasm events
// Maps to: cw20_transfers table
type CW20Transfer struct {
	ID              int64             `db:"id" json:"id"`
	TxHash          string            `db:"tx_hash" json:"tx_hash"`
	MsgIndex        int               `db:"msg_index" json:"msg_index"`
	Height          int64             `db:"height" json:"height"`
	ContractAddress string            `db:"contract_address" json:"contract_address"`
	Action          string            `db:"action" json:"action"` // transfer, send, mint, burn
	FromAddress     string            `db:"from_address" json:"from_address"`
	ToAddress       string            `db:"to_address" json:"to_address"`
	Amount          string            `db:"amount" json:"amount"`
	Memo            string            `db:"memo" json:"memo"`
	RawAttributes   map[string]string `db:"raw_attributes" json:"raw_attributes"`
	Timestamp       time.Time         `db:"timestamp" json:"timestamp"`
}

// BankTransfer represents a native token transfer
// Maps to: bank_transfers table
type BankTransfer struct {
	ID          int64     `db:"id" json:"id"`
	TxHash      string    `db:"tx_hash" json:"tx_hash"`
	MsgIndex    int       `db:"msg_index" json:"msg_index"`
	Height      int64     `db:"height" json:"height"`
	FromAddress string    `db:"from_address" json:"from_address"`
	ToAddress   string    `db:"to_address" json:"to_address"`
	Amount      string    `db:"amount" json:"amount"`
	Denom       string    `db:"denom" json:"denom"`
	AmountValue string    `db:"amount_value" json:"amount_value"`
	Timestamp   time.Time `db:"timestamp" json:"timestamp"`
}

// WasmCoin represents a coin in CosmWasm messages
type WasmCoin struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

// =============================================================================
// WASM MESSAGE TYPE CONSTANTS
// =============================================================================

const (
	MsgExecuteContract      = "/cosmwasm.wasm.v1.MsgExecuteContract"
	MsgInstantiateContract  = "/cosmwasm.wasm.v1.MsgInstantiateContract"
	MsgInstantiateContract2 = "/cosmwasm.wasm.v1.MsgInstantiateContract2"
	MsgMigrateContract      = "/cosmwasm.wasm.v1.MsgMigrateContract"
	MsgStoreCode            = "/cosmwasm.wasm.v1.MsgStoreCode"
	MsgUpdateAdmin          = "/cosmwasm.wasm.v1.MsgUpdateAdmin"
	MsgClearAdmin           = "/cosmwasm.wasm.v1.MsgClearAdmin"
	MsgSend                 = "/cosmos.bank.v1beta1.MsgSend"
	MsgMultiSend            = "/cosmos.bank.v1beta1.MsgMultiSend"
)

// =============================================================================
// WASM EVENT TYPE CONSTANTS
// =============================================================================

const (
	EventTypeWasm         = "wasm"
	EventTypeTransfer     = "transfer"
	EventTypeCoinReceived = "coin_received"
	EventTypeCoinSpent    = "coin_spent"
	EventTypeMessage      = "message"
	EventTypeInstantiate  = "instantiate"
	EventTypeMigrate      = "migrate"
	EventTypeReply        = "reply"
	EventTypeExecute      = "execute"
)

// =============================================================================
// CW20 ACTION CONSTANTS
// =============================================================================

const (
	CW20ActionTransfer          = "transfer"
	CW20ActionTransferFrom      = "transfer_from"
	CW20ActionSend              = "send"
	CW20ActionSendFrom          = "send_from"
	CW20ActionMint              = "mint"
	CW20ActionBurn              = "burn"
	CW20ActionBurnFrom          = "burn_from"
	CW20ActionIncreaseAllowance = "increase_allowance"
	CW20ActionDecreaseAllowance = "decrease_allowance"
)
