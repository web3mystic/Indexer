package parser

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cosmos-indexer/pkg/types"
)

// WasmParser handles decoding of CosmWasm messages and events
// into normalized indexer domain models.
type WasmParser struct{}

// NewWasmParser returns a ready-to-use WasmParser.
func NewWasmParser() *WasmParser {
	return &WasmParser{}
}

// ============================================================================
// TYPE CHECKERS
// ============================================================================

// IsWasmMessage reports whether the typeURL belongs to the cosmwasm module.
func (w *WasmParser) IsWasmMessage(typeURL string) bool {
	return strings.Contains(typeURL, "cosmwasm.wasm")
}

// IsBankMessage reports whether the typeURL belongs to the bank module.
func (w *WasmParser) IsBankMessage(typeURL string) bool {
	return strings.Contains(typeURL, "cosmos.bank")
}

// ============================================================================
// ParseExecuteContract
// ============================================================================
// ParseExecuteContract decodes a MsgExecuteContract protobuf payload
// and converts it into a WasmExecution model.
//
// The execute message (rawMsg) is JSON-decoded to extract the top-level
// action name. If JSON decoding fails, the raw bytes are stored as base64
// to avoid data loss.

func (w *WasmParser) ParseExecuteContract(
	msgBytes []byte,
	txHash string, msgIndex int, height int64,
	gasUsed int64, success bool, errMsg string,
	ts time.Time,
) (*types.WasmExecution, error) {

	sender, contract, rawMsg, funds, err := decodeExecuteContractProto(msgBytes)
	if err != nil {
		return nil, fmt.Errorf("decode MsgExecuteContract: %w", err)
	}

	execMsg := make(map[string]interface{})
	if jsonErr := json.Unmarshal(rawMsg, &execMsg); jsonErr != nil {
		execMsg = map[string]interface{}{
			"_raw": base64.StdEncoding.EncodeToString(rawMsg),
		}
	}

	action := ""
	if len(execMsg) == 1 {
		for k := range execMsg {
			action = k
		}
	}

	return &types.WasmExecution{
		TxHash:          txHash,
		MsgIndex:        msgIndex,
		Height:          height,
		Sender:          sender,
		ContractAddress: contract,
		ExecuteMsg:      execMsg,
		ExecuteAction:   action,
		Funds:           funds,
		GasUsed:         gasUsed,
		Success:         success,
		Error:           errMsg,
		Timestamp:       ts,
	}, nil
}

// ============================================================================
// ParseInstantiateContract
// ============================================================================
// ParseInstantiateContract decodes MsgInstantiateContract and builds
// a WasmInstantiation model.
// The contract address is not present in the protobuf message itself,
// so it is extracted from transaction events.

func (w *WasmParser) ParseInstantiateContract(
	msgBytes []byte,
	txHash string, msgIndex int, height int64,
	success bool, errMsg string,
	ts time.Time,
	events []types.ParsedEvent,
) (*types.WasmInstantiation, error) {

	sender, admin, codeID, label, rawMsg, funds, err := decodeInstantiateContractProto(msgBytes)
	if err != nil {
		return nil, fmt.Errorf("decode MsgInstantiateContract: %w", err)
	}

	initMsg := make(map[string]interface{})
	if jsonErr := json.Unmarshal(rawMsg, &initMsg); jsonErr != nil {
		initMsg = map[string]interface{}{
			"_raw": base64.StdEncoding.EncodeToString(rawMsg),
		}
	}

	contractAddress := extractContractAddressFromEvents(events)

	return &types.WasmInstantiation{
		TxHash:          txHash,
		MsgIndex:        msgIndex,
		Height:          height,
		Creator:         sender,
		Admin:           admin,
		CodeID:          codeID,
		Label:           label,
		ContractAddress: contractAddress,
		InitMsg:         initMsg,
		Funds:           funds,
		Success:         success,
		Error:           errMsg,
		Timestamp:       ts,
	}, nil
}

// ============================================================================
// ParseMigrateContract
// ============================================================================
// ParseMigrateContract decodes MsgMigrateContract.
// OldCodeID is intentionally left as 0 here and is populated later
// by the coordinator after querying the current contract state.

func (w *WasmParser) ParseMigrateContract(
	msgBytes []byte,
	txHash string, msgIndex int, height int64,
	success bool, errMsg string,
	ts time.Time,
) (*types.WasmMigration, error) {

	sender, contract, newCodeID, rawMsg, err := decodeMigrateContractProto(msgBytes)
	if err != nil {
		return nil, fmt.Errorf("decode MsgMigrateContract: %w", err)
	}

	migrateMsg := make(map[string]interface{})
	if jsonErr := json.Unmarshal(rawMsg, &migrateMsg); jsonErr != nil {
		migrateMsg = map[string]interface{}{
			"_raw": base64.StdEncoding.EncodeToString(rawMsg),
		}
	}

	return &types.WasmMigration{
		TxHash:          txHash,
		MsgIndex:        msgIndex,
		Height:          height,
		Sender:          sender,
		ContractAddress: contract,
		OldCodeID:       0, // filled by coordinator via GetWasmContract
		NewCodeID:       newCodeID,
		MigrateMsg:      migrateMsg,
		Success:         success,
		Error:           errMsg,
		Timestamp:       ts,
	}, nil
}

// ============================================================================
// ParseStoreCode
// ===========================================================================
// ParseStoreCode decodes a MsgStoreCode protobuf payload and builds a
func (w *WasmParser) ParseStoreCode(
	msgBytes []byte,
	txHash string, height int64,
	codeID int64,
	ts time.Time,
	events []types.ParsedEvent,
) (*types.WasmCode, error) {
	sender, checksum, permission, err := decodeStoreCodeProto(msgBytes)
	if err != nil {
		return nil, fmt.Errorf("decode MsgStoreCode: %w", err)
	}

	// If the proto did not carry a checksum, try the "store_code" event.
	for _, ev := range events {
		if ev.Type == "store_code" {
			if v, ok := ev.Attributes["code_checksum"]; ok {
				checksum = v
				break
			}
		}
	}

	// to its global default — which on this chain is "Everybody".
	if permission == "" {
		permission = "Everybody"
	}

	return &types.WasmCode{
		CodeID:         codeID,
		Creator:        sender,
		Checksum:       checksum,
		Permission:     permission,
		UploadedHeight: height,
		UploadedTime:   ts,
		UploadTxHash:   txHash,
	}, nil
}

// extractCodeIDFromEvents reads the code_id emitted by the "store_code" event.
// Returns 0 if not found; caller should treat 0 as "unknown".
func ExtractCodeIDFromEvents(events []types.ParsedEvent) int64 {
	for _, ev := range events {
		if ev.Type == "store_code" {
			if v, ok := ev.Attributes["code_id"]; ok {
				if id, err := strconv.ParseInt(v, 10, 64); err == nil {
					return id
				}
			}
		}
	}
	return 0
}

// decodeStoreCodeProto minimally decodes MsgStoreCode wire bytes.
//
// MsgStoreCode protobuf layout (cosmwasm/wasm/v1/tx.proto)
//
//	field 1 (string)  sender
//	field 2 (bytes)   wasm_byte_code   — skipped (can be megabytes)
//	field 3 (message) instantiate_permission
//	  └─ field 1 (varint) permission  (AccessType enum)
//	  └─ field 2 (string) address     (permitted address, deprecated in newer versions)
//	  └─ field 3 (string) addresses   (repeated, newer versions)
//
// We skip field 2 (wasm_byte_code) to avoid loading the full binary into
// memory. checksum is not in the proto message; it comes from chain events.
func decodeStoreCodeProto(data []byte) (sender, checksum, permission string, err error) {
	i := 0
	for i < len(data) {
		tag, n := decodeVarint(data[i:])
		if n == 0 {
			break
		}
		i += n
		fieldNum, wireType := tag>>3, tag&0x7
		switch {
		case fieldNum == 1 && wireType == 2: // sender
			s, nn := decodeLenDelim(data[i:])
			i += nn
			sender = string(s)
		case fieldNum == 2 && wireType == 2: // wasm_byte_code — skip entirely
			_, nn := decodeLenDelim(data[i:])
			i += nn
		case fieldNum == 3 && wireType == 2: // instantiate_permission (sub-message)
			sub, nn := decodeLenDelim(data[i:])
			i += nn
			// Decode the nested AccessConfig message
			j := 0
			for j < len(sub) {
				stag, sn := decodeVarint(sub[j:])
				if sn == 0 {
					break
				}
				j += sn
				sfieldNum, swireType := stag>>3, stag&0x7
				switch {
				case sfieldNum == 1 && swireType == 0: // AccessType enum
					v, snn := decodeVarint(sub[j:])
					j += snn
					permission = strconv.FormatUint(v, 10)
				case sfieldNum == 2 && swireType == 2: // address (deprecated single)
					_, snn := decodeLenDelim(sub[j:])
					j += snn
				case sfieldNum == 3 && swireType == 2: // addresses (repeated) — skip
					_, snn := decodeLenDelim(sub[j:])
					j += snn
				default:
					snn, serr := skipField(sub[j:], swireType)
					if serr != nil {
						break
					}
					j += snn
				}
			}
		default:
			nn, ferr := skipField(data[i:], wireType)
			if ferr != nil {
				return "", "", "", ferr
			}
			i += nn
		}
	}
	return
}

// ============================================================================
// ExtractWasmEvents
// ============================================================================
// isWasmEventType returns true for every event type that CosmWasm emits and
// that we want to record in the wasm_events table.
// All constants (EventTypeWasm, EventTypeInstantiate, EventTypeExecute,
// EventTypeMigrate, EventTypeReply) are already defined in types/types.go.

func isWasmEventType(t string) bool {
	switch t {
	case types.EventTypeWasm, // "wasm"        — contract custom events
		types.EventTypeInstantiate, // "instantiate" — MsgInstantiateContract
		types.EventTypeExecute,     // "execute"     — MsgExecuteContract
		types.EventTypeMigrate,     // "migrate"     — MsgMigrateContract
		types.EventTypeReply:       // "reply"       — sub-message replies
		return true
	}
	// Some chains also emit "wasm-transfer", "wasm-mint" etc.
	return strings.HasPrefix(t, "wasm-")
}

// ExtractWasmEvents filters a tx's ParsedEvents and converts every CosmWasm
// event (instantiate / execute / migrate / wasm / reply / wasm-*) into a
// []*WasmEvent ready to be stored in wasm_events.
func (w *WasmParser) ExtractWasmEvents(
	events []types.ParsedEvent,
	txHash string, height int64, ts time.Time,
) []*types.WasmEvent {

	var out []*types.WasmEvent

	for i, event := range events {
		if !isWasmEventType(event.Type) {
			continue
		}

		attrs := event.Attributes

		// Derive the message index from the attribute CosmWasm injects.
		// If absent (older chains), fall back to the event's position in the slice.
		msgIndex := 0
		if mi, ok := attrs["msg_index"]; ok {
			if parsed, err := strconv.Atoi(mi); err == nil {
				msgIndex = parsed
			}
		}

		out = append(out, &types.WasmEvent{
			TxHash:          txHash,
			MsgIndex:        msgIndex,
			EventIndex:      i,
			Height:          height,
			ContractAddress: attrs["_contract_address"],
			Action:          coalesceAction(event.Type, attrs),
			RawAttributes:   attrs,
			Timestamp:       ts,
		})
	}

	return out
}

// coalesceAction returns the best "action" string for a wasm event.
// For "wasm" events the contract sets an explicit "action" attribute.
// For "instantiate"/"execute"/"migrate" SDK events, use the event type itself
// so the action column is never blank.
func coalesceAction(eventType string, attrs map[string]string) string {
	if action, ok := attrs["action"]; ok && action != "" {
		return action
	}
	return eventType
}

// ============================================================================
// ParseCW20Transfers
// ============================================================================
// ParseCW20Transfers scans wasm events and extracts CW20 token transfers
// (transfer, send, mint, burn, etc.) into normalized transfer records.

func (w *WasmParser) ParseCW20Transfers(
	events []*types.WasmEvent,
	txHash string, height int64,
	ts time.Time,
) ([]*types.CW20Transfer, error) {

	cw20Actions := map[string]bool{
		types.CW20ActionTransfer:     true,
		types.CW20ActionTransferFrom: true,
		types.CW20ActionSend:         true,
		types.CW20ActionSendFrom:     true,
		types.CW20ActionMint:         true,
		types.CW20ActionBurn:         true,
		types.CW20ActionBurnFrom:     true,
	}

	var out []*types.CW20Transfer

	for _, event := range events {
		action := strings.ToLower(event.Action)
		if !cw20Actions[action] {
			continue
		}

		attrs := event.RawAttributes

		// Some CW20 implementations use "owner" instead of "from"
		from := attrs["from"]
		if from == "" {
			from = attrs["owner"]
		}

		// Some CW20 implementations use "recipient" instead of "to"
		to := attrs["to"]
		if to == "" {
			to = attrs["recipient"]
		}

		out = append(out, &types.CW20Transfer{
			TxHash:          txHash,
			MsgIndex:        event.MsgIndex,
			Height:          height,
			ContractAddress: event.ContractAddress,
			Action:          action,
			FromAddress:     from,
			ToAddress:       to,
			Amount:          attrs["amount"],
			Memo:            attrs["memo"],
			RawAttributes:   attrs,
			Timestamp:       ts,
		})
	}

	return out, nil
}

// ============================================================================
// ParseBankTransfers
// ============================================================================

func (w *WasmParser) ParseBankTransfers(
	events []types.ParsedEvent,
	txHash string, msgIndex int, height int64,
	ts time.Time,
) ([]*types.BankTransfer, error) {

	var out []*types.BankTransfer

	for _, event := range events {
		if event.Type != types.EventTypeTransfer {
			continue
		}

		from := event.Attributes["sender"]
		to := event.Attributes["recipient"]
		rawAmount := event.Attributes["amount"]

		if from == "" || to == "" || rawAmount == "" {
			continue
		}

		coins := parseCoins(rawAmount)

		for _, c := range coins {
			out = append(out, &types.BankTransfer{
				TxHash:      txHash,
				MsgIndex:    msgIndex,
				Height:      height,
				FromAddress: from,
				ToAddress:   to,
				Amount:      c.Amount + c.Denom,
				Denom:       c.Denom,
				AmountValue: c.Amount,
				Timestamp:   ts,
			})
		}

	}

	return out, nil
}

// ============================================================================
// MINIMAL PROTOBUF DECODERS
// ============================================================================
// The following functions implement minimal protobuf wire decoding for CosmWasm messages.
// Instead of relying on generated protobuf types, we manually parse
// only the required fields using protobuf wire format rules.
// This keeps the parser lightweight and avoids heavy proto dependencies.
// NOTE: These decoders intentionally ignore unknown fields.

func decodeExecuteContractProto(data []byte) (sender, contract string, msg []byte, funds []types.WasmCoin, err error) {
	i := 0
	for i < len(data) {
		tag, n := decodeVarint(data[i:])
		if n == 0 {
			break
		}
		i += n
		fieldNum, wireType := tag>>3, tag&0x7

		switch {
		case fieldNum == 1 && wireType == 2:
			s, nn := decodeLenDelim(data[i:])
			i += nn
			sender = string(s)
		case fieldNum == 2 && wireType == 2:
			s, nn := decodeLenDelim(data[i:])
			i += nn
			contract = string(s)
		case fieldNum == 3 && wireType == 2:
			s, nn := decodeLenDelim(data[i:])
			i += nn
			msg = s
		case fieldNum == 5 && wireType == 2:
			s, nn := decodeLenDelim(data[i:])
			i += nn
			funds = append(funds, decodeCoin(s))
		default:
			nn, e := skipField(data[i:], wireType)
			if e != nil {
				return "", "", nil, nil, e
			}
			i += nn
		}
	}
	return
}

func decodeInstantiateContractProto(data []byte) (sender, admin string, codeID int64, label string, msg []byte, funds []types.WasmCoin, err error) {
	i := 0
	for i < len(data) {
		tag, n := decodeVarint(data[i:])
		if n == 0 {
			break
		}
		i += n
		fieldNum, wireType := tag>>3, tag&0x7

		switch {
		case fieldNum == 1 && wireType == 2:
			s, nn := decodeLenDelim(data[i:])
			i += nn
			sender = string(s)
		case fieldNum == 2 && wireType == 2:
			s, nn := decodeLenDelim(data[i:])
			i += nn
			admin = string(s)
		case fieldNum == 3 && wireType == 0:
			v, nn := decodeVarint(data[i:])
			i += nn
			codeID = int64(v)
		case fieldNum == 4 && wireType == 2:
			s, nn := decodeLenDelim(data[i:])
			i += nn
			label = string(s)
		case fieldNum == 5 && wireType == 2:
			s, nn := decodeLenDelim(data[i:])
			i += nn
			msg = s
		case fieldNum == 6 && wireType == 2:
			s, nn := decodeLenDelim(data[i:])
			i += nn
			funds = append(funds, decodeCoin(s))
		default:
			nn, e := skipField(data[i:], wireType)
			if e != nil {
				return "", "", 0, "", nil, nil, e
			}
			i += nn
		}
	}
	return
}

func decodeMigrateContractProto(data []byte) (sender, contract string, newCodeID int64, msg []byte, err error) {
	i := 0
	for i < len(data) {
		tag, n := decodeVarint(data[i:])
		if n == 0 {
			break
		}
		i += n
		fieldNum, wireType := tag>>3, tag&0x7

		switch {
		case fieldNum == 1 && wireType == 2:
			s, nn := decodeLenDelim(data[i:])
			i += nn
			sender = string(s)
		case fieldNum == 2 && wireType == 2:
			s, nn := decodeLenDelim(data[i:])
			i += nn
			contract = string(s)
		case fieldNum == 3 && wireType == 0:
			v, nn := decodeVarint(data[i:])
			i += nn
			newCodeID = int64(v)
		case fieldNum == 4 && wireType == 2:
			s, nn := decodeLenDelim(data[i:])
			i += nn
			msg = s
		default:
			nn, e := skipField(data[i:], wireType)
			if e != nil {
				return "", "", 0, nil, e
			}
			i += nn
		}
	}
	return
}

func decodeCoin(data []byte) types.WasmCoin {
	var denom, amount string
	i := 0
	for i < len(data) {
		tag, n := decodeVarint(data[i:])
		if n == 0 {
			break
		}
		i += n
		if tag&0x7 == 2 {
			s, nn := decodeLenDelim(data[i:])
			i += nn
			switch tag >> 3 {
			case 1:
				denom = string(s)
			case 2:
				amount = string(s)
			}
		} else {
			nn, _ := skipField(data[i:], tag&0x7)
			i += nn
		}
	}
	return types.WasmCoin{Denom: denom, Amount: amount}
}

// decodeVarint reads a protobuf varint from data.
func decodeVarint(data []byte) (uint64, int) {
	var x uint64
	var s uint
	for i, b := range data {
		if i == 10 {
			return 0, 0
		}
		x |= uint64(b&0x7f) << s
		s += 7
		if b < 0x80 {
			return x, i + 1
		}
	}
	return 0, 0
}

func decodeLenDelim(data []byte) ([]byte, int) {
	length, n := decodeVarint(data)
	if n == 0 {
		return nil, 0
	}
	end := n + int(length)
	if end > len(data) {
		return nil, len(data)
	}
	return data[n:end], end
}

// skipField advances the cursor over an unknown protobuf field
// based on its wire type.
func skipField(data []byte, wireType uint64) (int, error) {
	switch wireType {
	case 0:
		_, n := decodeVarint(data)
		return n, nil
	case 1:
		return 8, nil
	case 2:
		_, n := decodeLenDelim(data)
		return n, nil
	case 5:
		return 4, nil
	default:
		return 0, fmt.Errorf("unknown proto wire type %d", wireType)
	}
}

// ============================================================================
// PRIVATE HELPERS
// ============================================================================
// extractContractAddressFromEvents retrieves the contract address
// emitted during instantiation or wasm execution events.

func extractContractAddressFromEvents(events []types.ParsedEvent) string {
	for _, event := range events {
		if event.Type == types.EventTypeInstantiate || event.Type == types.EventTypeWasm {
			if addr, ok := event.Attributes["_contract_address"]; ok {
				return addr
			}
		}
	}
	return ""
}

// parseCoinString splits a coin string like "1000uatom"
// into denom="uatom" and amount="1000".
//
//	func parseCoinString(s string) (denom, amount string) {
//		for i, c := range s {
//			if c < '0' || c > '9' {
//				return s[i:], s[:i]
//			}
//		}
//		return "", s
//	}
func parseCoins(s string) []struct {
	Denom  string
	Amount string
} {
	var result []struct {
		Denom  string
		Amount string
	}

	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		for i, c := range part {
			if c < '0' || c > '9' {
				result = append(result, struct {
					Denom  string
					Amount string
				}{
					Denom:  part[i:],
					Amount: part[:i],
				})
				break
			}
		}
	}

	return result
}
