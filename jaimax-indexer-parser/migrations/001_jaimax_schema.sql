-- =============================================================================
-- JAIMAX-1 INDEXER SCHEMA
-- =============================================================================
-- This schema powers the jaimax-1 PoA blockchain indexer.
--
-- Design principles:
--   • Idempotent writes (safe re-indexing & crash recovery)
--   • Snapshot-style state tables (validators, balances, authorities)
--   • Historical tracking via dedicated history tables
--   • JSONB storage for flexible message/event querying
--   • Trigger-driven registry synchronization (CosmWasm)
--   • PoA authority-controlled validator model (no delegations)
--
-- Optimized for:
--   • Explorer APIs
--   • Analytics queries
--   • High read throughput
-- =============================================================================


-- =============================================================================
-- CORE: BLOCKS TABLE
-- Blocks table is append-only and serves as the canonical chain timeline.
-- Height is the primary ordering mechanism for all indexing operations.
-- =============================================================================
CREATE TABLE IF NOT EXISTS blocks (
    height BIGINT PRIMARY KEY,
    hash VARCHAR(64) NOT NULL,
    time TIMESTAMP NOT NULL,
    proposer_address VARCHAR(128),
    tx_count INTEGER DEFAULT 0,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_blocks_time ON blocks(time DESC);
CREATE INDEX idx_blocks_proposer ON blocks(proposer_address);

COMMENT ON TABLE blocks IS 'Blockchain blocks from jaimax-1 chain';

-- =============================================================================
-- CORE: TRANSACTIONS TABLE
-- Transactions reference blocks via height (ON DELETE CASCADE ensures
-- full block rollback consistency if needed).
--
-- msg_types and addresses are stored as JSONB arrays to allow
-- fast GIN-based filtering without strict normalization.
-- =============================================================================
CREATE TABLE IF NOT EXISTS transactions (
    hash VARCHAR(64) PRIMARY KEY,
    height BIGINT NOT NULL,
    tx_index INTEGER NOT NULL,
    gas_used BIGINT DEFAULT 0,
    gas_wanted BIGINT DEFAULT 0,
    fee TEXT,
    success BOOLEAN DEFAULT FALSE,
    code INTEGER DEFAULT 0,
    log TEXT,
    memo TEXT,
    msg_types JSONB,
    addresses JSONB,
    timestamp TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW(),
    
    FOREIGN KEY (height) REFERENCES blocks(height) ON DELETE CASCADE
);

CREATE INDEX idx_tx_height ON transactions(height DESC);
CREATE INDEX idx_tx_success ON transactions(success);
CREATE INDEX idx_tx_timestamp ON transactions(timestamp DESC);
CREATE INDEX idx_tx_msg_types ON transactions USING GIN(msg_types);

COMMENT ON TABLE transactions IS 'Transactions on jaimax-1 chain';

-- =============================================================================
-- CORE: MESSAGES TABLE
-- =============================================================================
CREATE TABLE IF NOT EXISTS messages (
    id SERIAL PRIMARY KEY,
    tx_hash VARCHAR(64) NOT NULL,
    msg_index INTEGER NOT NULL,
    msg_type VARCHAR(255) NOT NULL,
    sender VARCHAR(128),
    receiver VARCHAR(128),
    amount TEXT,
    denom VARCHAR(64),
    raw_data JSONB,
    created_at TIMESTAMP DEFAULT NOW(),
    
    FOREIGN KEY (tx_hash) REFERENCES transactions(hash) ON DELETE CASCADE,
    UNIQUE(tx_hash, msg_index)
);

CREATE INDEX idx_msg_tx_hash ON messages(tx_hash);
CREATE INDEX idx_msg_type ON messages(msg_type);
CREATE INDEX idx_msg_sender ON messages(sender);
CREATE INDEX idx_msg_receiver ON messages(receiver);

COMMENT ON TABLE messages IS 'Decoded messages from transactions';

-- =============================================================================
-- CORE: EVENTS TABLE
-- =============================================================================
CREATE TABLE IF NOT EXISTS events (
    id SERIAL PRIMARY KEY,
    tx_hash VARCHAR(64) NOT NULL,
    event_index INTEGER NOT NULL,
    event_type VARCHAR(255) NOT NULL,
    attributes JSONB,
    created_at TIMESTAMP DEFAULT NOW(),
    
    FOREIGN KEY (tx_hash) REFERENCES transactions(hash) ON DELETE CASCADE,
    UNIQUE(tx_hash, event_index)
);

CREATE INDEX idx_event_tx_hash ON events(tx_hash);
CREATE INDEX idx_event_type ON events(event_type);
CREATE INDEX idx_event_attributes ON events USING GIN(attributes);

COMMENT ON TABLE events IS 'Blockchain events from jaimax-1';

-- =============================================================================
-- CORE: ADDRESS LOOKUP TABLE
-- Denormalized lookup table for fast address history queries.
-- Avoids expensive joins on large transactions tables.
-- =============================================================================
CREATE TABLE IF NOT EXISTS address_transactions (
    address VARCHAR(128) NOT NULL,
    tx_hash VARCHAR(64) NOT NULL,
    height BIGINT NOT NULL,
    created_at TIMESTAMP DEFAULT NOW(),
    
    PRIMARY KEY (address, tx_hash),
    FOREIGN KEY (tx_hash) REFERENCES transactions(hash) ON DELETE CASCADE
);

CREATE INDEX idx_addr_tx_address ON address_transactions(address);
CREATE INDEX idx_addr_tx_height ON address_transactions(height DESC);

COMMENT ON TABLE address_transactions IS 'Address to transaction mapping for quick lookup';

-- =============================================================================
-- CORE: INDEXER STATE TABLE
-- Single-row state table used for resumable indexing.
-- Ensures safe restarts after crashes.
-- =============================================================================
CREATE TABLE IF NOT EXISTS indexer_state (
    id INTEGER PRIMARY KEY DEFAULT 1,
    last_height BIGINT NOT NULL DEFAULT 0,
    last_block_hash VARCHAR(64),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    
    CONSTRAINT single_row_check CHECK (id = 1)
);

INSERT INTO indexer_state (id, last_height, last_block_hash)
VALUES (1, 0, '')
ON CONFLICT (id) DO NOTHING;

COMMENT ON TABLE indexer_state IS 'Tracks jaimax indexer progress';

-- =============================================================================
-- CORE: SYNC_STATE
-- Separate sync-state tables allow independent module syncing
-- (validators, governance, authority) without blocking block indexing.
-- =============================================================================

-- GOVERNANCE: PROPOSAL SYNC STATE
CREATE TABLE IF NOT EXISTS proposal_sync_state (
    id INTEGER PRIMARY KEY DEFAULT 1,
    last_sync_height BIGINT NOT NULL DEFAULT 0,
    last_sync_time TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

    CONSTRAINT single_row_check CHECK (id = 1)
);

INSERT INTO proposal_sync_state (id, last_sync_height)
VALUES (1, 0)
ON CONFLICT (id) DO NOTHING;


COMMENT ON TABLE proposal_sync_state
IS 'Tracks governance proposal sync progress';


-- VALIDATOR SYNC STATE
CREATE TABLE IF NOT EXISTS validator_sync_state (
    id INTEGER PRIMARY KEY DEFAULT 1,
    last_sync_height BIGINT NOT NULL DEFAULT 0,
    last_sync_time TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

    CONSTRAINT single_row_check CHECK (id = 1)
);

INSERT INTO validator_sync_state (id, last_sync_height)
VALUES (1, 0)
ON CONFLICT (id) DO NOTHING;

COMMENT ON TABLE validator_sync_state
IS 'Tracks validator set sync progress';


-- AUTHORITY SYNC STATE
CREATE TABLE IF NOT EXISTS authority_sync_state (
    id INTEGER PRIMARY KEY DEFAULT 1,
    last_sync_height BIGINT NOT NULL DEFAULT 0,
    last_sync_time TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

    CONSTRAINT single_row_check CHECK (id = 1)
);

INSERT INTO authority_sync_state (id, last_sync_height)
VALUES (1, 0)
ON CONFLICT (id) DO NOTHING;

COMMENT ON TABLE authority_sync_state
IS 'Tracks authority params sync progress';


-- =============================================================================
-- POA: AUTHORITY ACCOUNTS TABLE 
-- Stores current PoA authority accounts.
-- In jaimax-1, authorities control validator lifecycle and power.
--
-- "source" indicates how the authority was added:
--   • genesis  – from initial chain config
--   • params   – from authority module params
--   • tx       – via governance or authority action
-- =============================================================================
CREATE TABLE IF NOT EXISTS authority_accounts (
    address VARCHAR(128) PRIMARY KEY,

    added_at TIMESTAMP NOT NULL,
    added_by VARCHAR(128),
    added_at_height BIGINT NOT NULL,

    active BOOLEAN DEFAULT TRUE,

    removed_at TIMESTAMP,
    removed_by VARCHAR(128),

    source VARCHAR(50) DEFAULT 'params', -- params / tx / genesis
    updated_at TIMESTAMP DEFAULT NOW(),

    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_authority_active ON authority_accounts(active);
CREATE INDEX idx_authority_added_height ON authority_accounts(added_at_height DESC);
CREATE INDEX idx_authority_address_active ON authority_accounts(address, active);

COMMENT ON TABLE authority_accounts
IS 'Current PoA authority accounts';



-- =============================================================================
-- AUTHORITY STATE 
-- Historical snapshot of authority set at specific heights.
-- Used for explorer time-travel features.
-- =============================================================================
CREATE TABLE IF NOT EXISTS authority_state (
    height BIGINT NOT NULL,
    address VARCHAR(128) NOT NULL,

    PRIMARY KEY (height, address)
);

CREATE INDEX idx_authority_state_height
ON authority_state(height DESC);

COMMENT ON TABLE authority_state
IS 'Authority set at specific block heights';






-- =============================================================================
-- POA: VALIDATORS TABLE
-- PoA validator model (no delegations).
-- Power is authority-controlled (not stake-based).
-- tokens are derived from power using fixed conversion.
-- =============================================================================
CREATE TABLE IF NOT EXISTS validators (
    operator_address VARCHAR(128) PRIMARY KEY,
    consensus_address VARCHAR(128) UNIQUE,
    consensus_pubkey TEXT,
    moniker VARCHAR(255),
    identity VARCHAR(64),
    website VARCHAR(255),
    security_contact VARCHAR(255),
    details TEXT,
    
-- PoA-specific fields
jailed BOOLEAN DEFAULT FALSE,
status VARCHAR(50) NOT NULL DEFAULT 'BOND_STATUS_UNBONDED', 
power BIGINT NOT NULL DEFAULT 0,
voting_power BIGINT NOT NULL DEFAULT 0,
tokens TEXT NOT NULL DEFAULT '0',
delegator_shares TEXT DEFAULT '0',   

    -- PoA authority tracking
    added_by VARCHAR(128),
    added_at TIMESTAMP,
    added_at_height BIGINT,
    
    -- Standard fields
    unbonding_height BIGINT,
    unbonding_time TIMESTAMP,
    commission_rate TEXT,
    commission_max_rate TEXT,
    commission_max_change_rate TEXT,
    min_self_delegation TEXT,
    
    updated_at TIMESTAMP DEFAULT NOW(),
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_validators_status ON validators(status);
CREATE INDEX idx_validators_jailed ON validators(jailed);
CREATE INDEX idx_validators_power ON validators(power DESC);
CREATE INDEX idx_validators_added_by ON validators(added_by);

COMMENT ON TABLE validators IS 'jaimax PoA validators - authority-managed, no delegations';

-- =============================================================================
-- POA: VALIDATOR HISTORY TABLE
-- Tracks validator status and power changes over time.
-- Automatically populated via trigger on validator updates.
-- =============================================================================
CREATE TABLE IF NOT EXISTS validator_history (
    id SERIAL PRIMARY KEY,
    operator_address VARCHAR(128) NOT NULL,
    height BIGINT NOT NULL,
    
    -- Status tracking
    status VARCHAR(50) NOT NULL,
    jailed BOOLEAN DEFAULT FALSE,
    power BIGINT DEFAULT 0,
    tokens TEXT,
    
    -- PoA: Who changed it
    changed_by VARCHAR(128),
    change_type VARCHAR(50),
    old_power BIGINT,
    new_power BIGINT,
    
    timestamp TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW(),
    
    FOREIGN KEY (operator_address) REFERENCES validators(operator_address) ON DELETE CASCADE
);

CREATE INDEX idx_validator_history_operator ON validator_history(operator_address);
CREATE INDEX idx_validator_history_height ON validator_history(height DESC);
CREATE INDEX idx_validator_history_timestamp ON validator_history(timestamp DESC);
CREATE INDEX idx_validator_history_changed_by ON validator_history(changed_by);

COMMENT ON TABLE validator_history IS 'Historical changes to validators (PoA authority actions)';

-- =============================================================================
-- POA: AUTHORITY ACTIONS TABLE 
-- Logs all authority-controlled operations affecting validators
-- or authority accounts.
--
-- This table is the audit trail for PoA governance.
-- =============================================================================
CREATE TABLE IF NOT EXISTS authority_actions (
    id SERIAL PRIMARY KEY,
    height BIGINT NOT NULL,
    tx_hash VARCHAR(64) NOT NULL,
    
    authority_addr VARCHAR(128) NOT NULL,
    validator_addr VARCHAR(128),
    action_type VARCHAR(50) NOT NULL,
    
    old_value TEXT,
    new_value TEXT,
    moniker VARCHAR(255),
    
    timestamp TIMESTAMP NOT NULL,
    success BOOLEAN DEFAULT TRUE,
    error_message TEXT,
    
    created_at TIMESTAMP DEFAULT NOW(),
    
    FOREIGN KEY (tx_hash) REFERENCES transactions(hash) ON DELETE CASCADE
);

CREATE INDEX idx_authority_actions_height ON authority_actions(height DESC);
CREATE INDEX idx_authority_actions_authority ON authority_actions(authority_addr);
CREATE INDEX idx_authority_actions_validator ON authority_actions(validator_addr);
CREATE INDEX idx_authority_actions_type ON authority_actions(action_type);
CREATE INDEX idx_authority_actions_timestamp ON authority_actions(timestamp DESC);

COMMENT ON TABLE authority_actions IS 'All PoA authority actions (add/remove validator, set power, update authorities)';

-- =============================================================================
-- BALANCES TABLE 
-- =============================================================================
CREATE TABLE IF NOT EXISTS balances (
    address VARCHAR(128) NOT NULL,
    denom VARCHAR(64) NOT NULL,
    amount TEXT NOT NULL,
    height BIGINT NOT NULL,
    updated_at TIMESTAMP DEFAULT NOW(),
    created_at TIMESTAMP DEFAULT NOW(),
    
    PRIMARY KEY (address, denom)
);

CREATE INDEX idx_balances_address ON balances(address);
CREATE INDEX idx_balances_denom ON balances(denom);
CREATE INDEX idx_balances_height ON balances(height DESC);

COMMENT ON TABLE balances IS 'Current account balances on jaimax-1';

-- =============================================================================
-- PROPOSALS TABLE (Governance if enabled)
-- =============================================================================
CREATE TABLE IF NOT EXISTS proposals (
    proposal_id BIGINT PRIMARY KEY,
    title VARCHAR(500) NOT NULL,
    description TEXT NOT NULL,
    proposal_type VARCHAR(100) NOT NULL,
    status VARCHAR(50) NOT NULL,
    
    submit_time TIMESTAMP NOT NULL,
    deposit_end_time TIMESTAMP NOT NULL,
    voting_start_time TIMESTAMP,
    voting_end_time TIMESTAMP,
    total_deposit TEXT,
    deposit_denom VARCHAR(64), 
    metadata TEXT,   
    messages JSONB,   
    yes_votes TEXT DEFAULT '0',
    no_votes TEXT DEFAULT '0',
    abstain_votes TEXT DEFAULT '0',
    no_with_veto_votes TEXT DEFAULT '0',
    
    proposer VARCHAR(128),
    height BIGINT NOT NULL,
    
    updated_at TIMESTAMP DEFAULT NOW(),
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_proposals_status ON proposals(status);
CREATE INDEX idx_proposals_submit_time ON proposals(submit_time DESC);
CREATE INDEX idx_proposals_proposer ON proposals(proposer);

COMMENT ON TABLE proposals IS 'Governance proposals on jaimax-1';

-- =============================================================================
-- VOTES TABLE (Governance)
-- =============================================================================
CREATE TABLE IF NOT EXISTS votes (
    id SERIAL PRIMARY KEY,
    proposal_id BIGINT NOT NULL,
    voter VARCHAR(128) NOT NULL,
    option VARCHAR(50) NOT NULL,
    options JSONB,
    height BIGINT NOT NULL,
    tx_hash VARCHAR(64),
    timestamp TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT NOW(),
    
    UNIQUE(proposal_id, voter),
    FOREIGN KEY (proposal_id) REFERENCES proposals(proposal_id) ON DELETE CASCADE,
    FOREIGN KEY (tx_hash) REFERENCES transactions(hash) ON DELETE SET NULL
);

CREATE INDEX idx_votes_proposal ON votes(proposal_id);
CREATE INDEX idx_votes_voter ON votes(voter);
CREATE INDEX idx_votes_timestamp ON votes(timestamp DESC);

COMMENT ON TABLE votes IS 'Governance votes on jaimax-1';

-- =============================================================================
-- COSMWASM: WASM CONTRACTS  (registry of every deployed contract)
-- Canonical registry of all deployed CosmWasm contracts.
-- Automatically synchronized via triggers on instantiation/migration.
-- =============================================================================
CREATE TABLE IF NOT EXISTS wasm_contracts (
    contract_address       VARCHAR(128) PRIMARY KEY,
    code_id                BIGINT       NOT NULL,
    creator                VARCHAR(128) NOT NULL,
    admin                  VARCHAR(128),
    label                  TEXT,
    init_msg               JSONB,
    contract_info          JSONB,
    instantiated_at_height BIGINT,
    instantiated_at_time   TIMESTAMP,
    instantiate_tx_hash    VARCHAR(64),
    current_code_id        BIGINT,
    last_migrated_height   BIGINT,
    last_migrated_tx_hash  VARCHAR(64),
    is_active              BOOLEAN      DEFAULT TRUE,
    updated_at             TIMESTAMP    DEFAULT NOW(),
    created_at             TIMESTAMP    DEFAULT NOW()
);

CREATE INDEX idx_contracts_code_id  ON wasm_contracts(code_id);
CREATE INDEX idx_contracts_creator  ON wasm_contracts(creator);
CREATE INDEX idx_contracts_label    ON wasm_contracts(label);
CREATE INDEX idx_contracts_active   ON wasm_contracts(is_active);
CREATE INDEX idx_contracts_height   ON wasm_contracts(instantiated_at_height DESC);

COMMENT ON TABLE wasm_contracts IS 'Registry of every CosmWasm contract on jaimax-1';

-- =============================================================================
-- COSMWASM: WASM CODES  (uploaded wasm binaries)
-- =============================================================================
CREATE TABLE IF NOT EXISTS wasm_codes (
    code_id         BIGINT       PRIMARY KEY,
    creator         VARCHAR(128) NOT NULL,
    checksum        VARCHAR(128),
    permission      VARCHAR(64),
    uploaded_height BIGINT,
    uploaded_time   TIMESTAMP,
    upload_tx_hash  VARCHAR(64),
    created_at      TIMESTAMP    DEFAULT NOW()
);

CREATE INDEX idx_codes_creator ON wasm_codes(creator);

COMMENT ON TABLE wasm_codes IS 'Uploaded CosmWasm code IDs';

-- =============================================================================
-- COSMWASM: WASM EXECUTIONS
-- High-volume table: every MsgExecuteContract call.
-- Indexed by contract, sender, action, and JSONB message content.
-- =============================================================================
CREATE TABLE IF NOT EXISTS wasm_executions (
    id               BIGSERIAL    PRIMARY KEY,
    tx_hash          VARCHAR(64)  NOT NULL REFERENCES transactions(hash) ON DELETE CASCADE,
    msg_index        INTEGER      NOT NULL,
    height           BIGINT       NOT NULL,
    sender           VARCHAR(128) NOT NULL,
    contract_address VARCHAR(128) NOT NULL,
    execute_msg      JSONB        NOT NULL,
    execute_action   VARCHAR(128),
    funds            JSONB,
    gas_used         BIGINT,
    success          BOOLEAN      DEFAULT TRUE,
    error            TEXT,
    timestamp        TIMESTAMP    NOT NULL,
    created_at       TIMESTAMP    DEFAULT NOW()
);

CREATE INDEX idx_exec_tx_hash         ON wasm_executions(tx_hash);
CREATE INDEX idx_exec_contract        ON wasm_executions(contract_address);
CREATE INDEX idx_exec_sender          ON wasm_executions(sender);
CREATE INDEX idx_exec_action          ON wasm_executions(execute_action);
CREATE INDEX idx_exec_height          ON wasm_executions(height DESC);
CREATE INDEX idx_exec_timestamp       ON wasm_executions(timestamp DESC);
CREATE INDEX idx_exec_contract_action ON wasm_executions(contract_address, execute_action);
CREATE INDEX idx_exec_msg             ON wasm_executions USING GIN(execute_msg);

COMMENT ON TABLE wasm_executions IS 'Every MsgExecuteContract call';

-- =============================================================================
-- COSMWASM: WASM INSTANTIATIONS  (MsgInstantiateContract)
-- =============================================================================
CREATE TABLE IF NOT EXISTS wasm_instantiations (
    id               BIGSERIAL    PRIMARY KEY,
    tx_hash          VARCHAR(64)  NOT NULL REFERENCES transactions(hash) ON DELETE CASCADE,
    msg_index        INTEGER      NOT NULL,
    height           BIGINT       NOT NULL,
    creator          VARCHAR(128) NOT NULL,
    admin            VARCHAR(128),
    code_id          BIGINT       NOT NULL,
    label            TEXT,
    contract_address VARCHAR(128),
    init_msg         JSONB        NOT NULL,
    funds            JSONB,
    success          BOOLEAN      DEFAULT TRUE,
    error            TEXT,
    timestamp        TIMESTAMP    NOT NULL,
    created_at       TIMESTAMP    DEFAULT NOW()
);

CREATE INDEX idx_instantiate_tx_hash  ON wasm_instantiations(tx_hash);
CREATE INDEX idx_instantiate_creator  ON wasm_instantiations(creator);
CREATE INDEX idx_instantiate_code_id  ON wasm_instantiations(code_id);
CREATE INDEX idx_instantiate_contract ON wasm_instantiations(contract_address);
CREATE INDEX idx_instantiate_height   ON wasm_instantiations(height DESC);

COMMENT ON TABLE wasm_instantiations IS 'Every MsgInstantiateContract — builds contract registry';

-- =============================================================================
-- COSMWASM: WASM MIGRATIONS  (MsgMigrateContract)
-- =============================================================================
CREATE TABLE IF NOT EXISTS wasm_migrations (
    id               BIGSERIAL    PRIMARY KEY,
    tx_hash          VARCHAR(64)  NOT NULL REFERENCES transactions(hash) ON DELETE CASCADE,
    msg_index        INTEGER      NOT NULL,
    height           BIGINT       NOT NULL,
    sender           VARCHAR(128) NOT NULL,
    contract_address VARCHAR(128) NOT NULL,
    old_code_id      BIGINT,
    new_code_id      BIGINT       NOT NULL,
    migrate_msg      JSONB,
    success          BOOLEAN      DEFAULT TRUE,
    error            TEXT,
    timestamp        TIMESTAMP    NOT NULL,
    created_at       TIMESTAMP    DEFAULT NOW()
);

CREATE INDEX idx_migrate_tx_hash  ON wasm_migrations(tx_hash);
CREATE INDEX idx_migrate_contract ON wasm_migrations(contract_address);
CREATE INDEX idx_migrate_height   ON wasm_migrations(height DESC);
CREATE INDEX idx_migrate_new_code ON wasm_migrations(new_code_id);

COMMENT ON TABLE wasm_migrations IS 'Every MsgMigrateContract — tracks contract upgrades';

-- =============================================================================
-- COSMWASM: WASM EVENTS  (structured wasm-type events)
-- =============================================================================
CREATE TABLE IF NOT EXISTS wasm_events (
    id               BIGSERIAL    PRIMARY KEY,
    tx_hash          VARCHAR(64)  NOT NULL REFERENCES transactions(hash) ON DELETE CASCADE,
    msg_index        INTEGER,
    event_index      INTEGER      NOT NULL,
    height           BIGINT       NOT NULL,
    contract_address VARCHAR(128),
    action           VARCHAR(128),
    raw_attributes   JSONB        NOT NULL,
    timestamp        TIMESTAMP    NOT NULL,
    created_at       TIMESTAMP    DEFAULT NOW()
);

CREATE INDEX idx_wasm_events_tx_hash   ON wasm_events(tx_hash);
CREATE INDEX idx_wasm_events_contract  ON wasm_events(contract_address);
CREATE INDEX idx_wasm_events_action    ON wasm_events(action);
CREATE INDEX idx_wasm_events_height    ON wasm_events(height DESC);
CREATE INDEX idx_wasm_events_timestamp ON wasm_events(timestamp DESC);
CREATE INDEX idx_wasm_events_attrs     ON wasm_events USING GIN(raw_attributes);
CREATE INDEX idx_wasm_events_ca_action ON wasm_events(contract_address, action);

COMMENT ON TABLE wasm_events IS 'Structured wasm-type events — entry point for all contract activity';

-- =============================================================================
-- COSMWASM: CW20 TOKEN TRANSFERS
-- Normalized CW20 token transfers extracted from wasm events.
-- amount stored as NUMERIC(78,0) for safe large integer handling.
-- =============================================================================
CREATE TABLE IF NOT EXISTS cw20_transfers (
    id               BIGSERIAL    PRIMARY KEY,
    tx_hash          VARCHAR(64)  NOT NULL REFERENCES transactions(hash) ON DELETE CASCADE,
    msg_index        INTEGER,
    height           BIGINT       NOT NULL,
    contract_address VARCHAR(128) NOT NULL,
    action           VARCHAR(64)  NOT NULL,   -- transfer, send, mint, burn, etc.
    from_address     VARCHAR(128),
    to_address       VARCHAR(128),
    amount           NUMERIC(78, 0),
    memo             TEXT,
    raw_attributes   JSONB,
    timestamp        TIMESTAMP    NOT NULL,
    created_at       TIMESTAMP    DEFAULT NOW()
);

CREATE INDEX idx_cw20_tx_hash  ON cw20_transfers(tx_hash);
CREATE INDEX idx_cw20_contract ON cw20_transfers(contract_address);
CREATE INDEX idx_cw20_from     ON cw20_transfers(from_address);
CREATE INDEX idx_cw20_to       ON cw20_transfers(to_address);
CREATE INDEX idx_cw20_action   ON cw20_transfers(action);
CREATE INDEX idx_cw20_height   ON cw20_transfers(height DESC);

COMMENT ON TABLE cw20_transfers IS 'CW20 token transfers parsed from wasm events';

-- =============================================================================
-- COSMWASM: BANK TRANSFERS  (native token — MsgSend / bank module events)
-- Native token transfers parsed from bank module events.
-- amount_value enables numeric aggregation and sorting.
-- =============================================================================
CREATE TABLE IF NOT EXISTS bank_transfers (
    id           BIGSERIAL    PRIMARY KEY,
    tx_hash      VARCHAR(64)  NOT NULL REFERENCES transactions(hash) ON DELETE CASCADE,
    msg_index    INTEGER,
    height       BIGINT       NOT NULL,
    from_address VARCHAR(128) NOT NULL,
    to_address   VARCHAR(128) NOT NULL,
    amount       TEXT         NOT NULL,
    denom        VARCHAR(64)  NOT NULL,
    amount_value NUMERIC(78, 0),
    timestamp    TIMESTAMP    NOT NULL,
    created_at   TIMESTAMP    DEFAULT NOW()
);

CREATE INDEX idx_bank_tx_hash ON bank_transfers(tx_hash);
CREATE INDEX idx_bank_from    ON bank_transfers(from_address);
CREATE INDEX idx_bank_to      ON bank_transfers(to_address);
CREATE INDEX idx_bank_denom   ON bank_transfers(denom);
CREATE INDEX idx_bank_height  ON bank_transfers(height DESC);

COMMENT ON TABLE bank_transfers IS 'Native token transfers from bank module';

-- =============================================================================
-- VIEWS SECTION
-- =============================================================================

-- Validator statistics (PoA-aware)
CREATE OR REPLACE VIEW validator_stats AS
SELECT 
    v.operator_address,
    v.moniker,
    v.status,
    v.jailed,
    v.power,
    v.tokens,
    v.added_by,
    v.added_at,
    COUNT(vh.id) as total_changes,
    MAX(vh.timestamp) as last_change,
    COUNT(b.height) as blocks_proposed,
    v.updated_at
FROM validators v
LEFT JOIN validator_history vh ON v.operator_address = vh.operator_address
LEFT JOIN blocks b ON v.consensus_address = b.proposer_address
GROUP BY v.operator_address, v.moniker, v.status, v.jailed, 
         v.power, v.tokens, v.added_by, v.added_at, v.updated_at;

-- Authority activity view
CREATE OR REPLACE VIEW authority_activity AS
SELECT 
    aa.authority_addr,
    COUNT(*) as total_actions,
    COUNT(*) FILTER (WHERE aa.action_type = 'add_validator') as validators_added,
    COUNT(*) FILTER (WHERE aa.action_type = 'remove_validator') as validators_removed,
    COUNT(*) FILTER (WHERE aa.action_type = 'set_validator_power') as power_changes,
    COUNT(*) FILTER (WHERE aa.action_type = 'update_authority_accounts') as authority_updates,
    MAX(aa.timestamp) as last_action,
    MIN(aa.timestamp) as first_action
FROM authority_actions aa
GROUP BY aa.authority_addr;

-- Chain statistics view
CREATE OR REPLACE VIEW chain_stats AS
SELECT 
    (SELECT COUNT(*) FROM blocks) as total_blocks,
    (SELECT COUNT(*) FROM transactions) as total_transactions,
    (SELECT COUNT(*) FROM transactions WHERE success = TRUE) as successful_txs,
    (SELECT COUNT(*) FROM validators WHERE status = 'BOND_STATUS_BONDED') as active_validators,
    (SELECT COUNT(*) FROM authority_accounts WHERE active = true) as active_authorities,
    (SELECT SUM(power) FROM validators WHERE status = 'BOND_STATUS_BONDED') as total_power,
    (SELECT COUNT(*) FROM authority_actions) as total_authority_actions,
    (SELECT COUNT(*) FROM wasm_contracts WHERE is_active = TRUE) as active_contracts,
    (SELECT COUNT(*) FROM wasm_executions) as total_wasm_executions,
    (SELECT COUNT(*) FROM wasm_instantiations) as total_instantiations,
    (SELECT COUNT(*) FROM cw20_transfers) as cw20_transfers_count,
    (SELECT COUNT(*) FROM bank_transfers) as bank_transfers_count,
    (SELECT MAX(height) FROM blocks) as latest_height,
    NOW() as generated_at;

-- Recent transactions for explorer API
CREATE OR REPLACE VIEW recent_transactions AS
SELECT
    t.hash,
    t.height,
    t.tx_index,
    t.success,
    t.gas_used,
    t.gas_wanted,
    t.fee,
    t.memo,
    t.timestamp,
    b.time AS block_time
FROM transactions t
JOIN blocks b ON b.height = t.height
ORDER BY t.height DESC, t.tx_index DESC;

-- Contract activity leaderboard
CREATE OR REPLACE VIEW contract_activity AS
SELECT
    c.contract_address,
    c.label,
    c.code_id,
    c.creator,
    COUNT(e.id)              AS total_executions,
    MAX(e.timestamp)         AS last_execution,
    COUNT(DISTINCT e.sender) AS unique_users
FROM wasm_contracts c
LEFT JOIN wasm_executions e ON c.contract_address = e.contract_address
GROUP BY c.contract_address, c.label, c.code_id, c.creator
ORDER BY total_executions DESC;

-- Recent wasm activity
CREATE OR REPLACE VIEW recent_wasm_activity AS
SELECT
    we.tx_hash,
    we.height,
    we.contract_address,
    wc.label                                AS contract_label,
    we.action,

    -- sender: try execution first, then instantiation, then migration
    COALESCE(
        wx.sender,
        wi.creator,
        wm.sender
    )                                       AS sender,

    -- recipient: contract-emitted attribute or contract address itself for deposits
    COALESCE(
        we.raw_attributes->>'recipient',
        we.raw_attributes->>'to',
        we.raw_attributes->>'to_address',
        CASE
            WHEN we.action IN ('deposit', 'instantiate', 'execute', 'wasm')
            THEN we.contract_address
        END
    )                                       AS recipient,

    we.raw_attributes->>'amount'            AS amount,
    we.timestamp

FROM wasm_events we
LEFT JOIN wasm_contracts      wc ON wc.contract_address = we.contract_address
LEFT JOIN wasm_executions     wx ON wx.tx_hash = we.tx_hash
                               AND wx.msg_index = we.msg_index
LEFT JOIN wasm_instantiations wi ON wi.tx_hash = we.tx_hash
                               AND wi.msg_index = we.msg_index
LEFT JOIN wasm_migrations     wm ON wm.tx_hash = we.tx_hash
                               AND wm.msg_index = we.msg_index
ORDER BY we.height DESC;

-- CW20 address activity
CREATE OR REPLACE VIEW cw20_address_activity AS
SELECT
    contract_address,
    from_address  AS address,
    COUNT(*)      AS transfers_sent,
    SUM(amount)   AS total_sent
FROM cw20_transfers
WHERE from_address IS NOT NULL
GROUP BY contract_address, from_address
UNION ALL
SELECT
    contract_address,
    to_address    AS address,
    0             AS transfers_sent,
    SUM(amount)   AS total_received
FROM cw20_transfers
WHERE to_address IS NOT NULL
GROUP BY contract_address, to_address;

-- =============================================================================
-- UTILITY FUNCTIONS
-- =============================================================================

-- Get transaction count by address
CREATE OR REPLACE FUNCTION get_tx_count_by_address(addr VARCHAR)
RETURNS BIGINT AS $$
    SELECT COUNT(*)
    FROM address_transactions
    WHERE address = addr;
$$ LANGUAGE SQL;

-- Active authorities (fast API)
CREATE OR REPLACE VIEW active_authorities AS
SELECT
    address,
    added_at,
    added_at_height
FROM authority_accounts
WHERE active = true;

-- Check if address is authority
CREATE OR REPLACE FUNCTION is_authority(addr VARCHAR)
RETURNS BOOLEAN AS $$
    SELECT EXISTS(
        SELECT 1 FROM authority_accounts
        WHERE address = addr AND active = true
    );
$$ LANGUAGE SQL;

-- Get validator power at specific height
CREATE OR REPLACE FUNCTION get_validator_power_at_height(val_addr VARCHAR, target_height BIGINT)
RETURNS BIGINT AS $$
    SELECT COALESCE(
        (
            SELECT new_power
            FROM validator_history
            WHERE operator_address = val_addr
              AND height <= target_height
            ORDER BY height DESC
            LIMIT 1
        ),
        0
    );
$$ LANGUAGE SQL;

-- Get total chain power (all bonded validators)
CREATE OR REPLACE FUNCTION get_total_chain_power()
RETURNS BIGINT AS $$
    SELECT COALESCE(SUM(power), 0)
    FROM validators
    WHERE status = 'BOND_STATUS_BONDED' AND jailed = false;
$$ LANGUAGE SQL;

-- Convert power to tokens (jaimax PoA: power * 1_000_000)
CREATE OR REPLACE FUNCTION power_to_tokens(power_val BIGINT)
RETURNS TEXT AS $$
    SELECT (power_val * 1000000)::TEXT;
$$ LANGUAGE SQL;

-- Convert tokens to power (jaimax PoA: tokens / 1_000_000)
CREATE OR REPLACE FUNCTION tokens_to_power(tokens_val TEXT)
RETURNS BIGINT AS $$
    SELECT (tokens_val::BIGINT / 1000000);
$$ LANGUAGE SQL;

-- Get contract execution count
CREATE OR REPLACE FUNCTION contract_exec_count(addr VARCHAR)
RETURNS BIGINT AS $$
    SELECT COUNT(*) FROM wasm_executions WHERE contract_address = addr;
$$ LANGUAGE SQL;

-- =============================================================================
-- TRIGGERS
-- =============================================================================

-- Update validator updated_at on change
CREATE OR REPLACE FUNCTION update_validator_timestamp()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_validator_updated
    BEFORE UPDATE ON validators
    FOR EACH ROW
    EXECUTE FUNCTION update_validator_timestamp();

-- Auto-create validator history entry on power change
CREATE OR REPLACE FUNCTION log_validator_change()
RETURNS TRIGGER AS $$
BEGIN
    IF (TG_OP = 'UPDATE' AND (OLD.power != NEW.power OR OLD.status != NEW.status)) THEN
        INSERT INTO validator_history (
            operator_address, height, status, jailed, power, tokens,
            old_power, new_power, timestamp
        )
        VALUES (
            NEW.operator_address,
            COALESCE((SELECT last_height FROM indexer_state WHERE id = 1), 0),
            NEW.status,
            NEW.jailed,
            NEW.power,
            NEW.tokens,
            OLD.power,
            NEW.power,
            NOW()
        );
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_log_validator_change
    AFTER UPDATE ON validators
    FOR EACH ROW
    EXECUTE FUNCTION log_validator_change();

-- Auto-populate wasm_contracts when a new instantiation succeeds
CREATE OR REPLACE FUNCTION sync_contract_on_instantiate()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.success = TRUE AND NEW.contract_address IS NOT NULL THEN
        INSERT INTO wasm_contracts (
            contract_address, code_id, creator, admin, label, init_msg,
            instantiated_at_height, instantiated_at_time, instantiate_tx_hash,
            current_code_id, contract_info
        ) VALUES (
            NEW.contract_address, NEW.code_id, NEW.creator, NEW.admin,
            NEW.label, NEW.init_msg,
            NEW.height, NEW.timestamp, NEW.tx_hash,
            NEW.code_id,
            jsonb_build_object(
                'code_id',         NEW.code_id,
                'creator',         NEW.creator,
                'admin',           COALESCE(NEW.admin, ''),
                'label',           COALESCE(NEW.label, ''),
                'is_active',       TRUE,
                'current_code_id', NEW.code_id
            )
        )
        ON CONFLICT (contract_address) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trigger_contract_registry ON wasm_instantiations;
CREATE TRIGGER trigger_contract_registry
    AFTER INSERT ON wasm_instantiations
    FOR EACH ROW EXECUTE FUNCTION sync_contract_on_instantiate();
    AFTER INSERT ON wasm_instantiations
    FOR EACH ROW EXECUTE FUNCTION sync_contract_on_instantiate();

-- Auto-update contract code_id on migration
CREATE OR REPLACE FUNCTION sync_contract_on_migrate()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.success = TRUE THEN
        UPDATE wasm_contracts
        SET current_code_id       = NEW.new_code_id,
            last_migrated_height  = NEW.height,
            last_migrated_tx_hash = NEW.tx_hash,
            updated_at            = NOW()
        WHERE contract_address = NEW.contract_address;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trigger_contract_migrate ON wasm_migrations;
CREATE TRIGGER trigger_contract_migrate
    AFTER INSERT ON wasm_migrations
    FOR EACH ROW EXECUTE FUNCTION sync_contract_on_migrate();

-- =============================================================================
-- COMPLETION MESSAGE
-- =============================================================================

DO $$
BEGIN
    RAISE NOTICE '✅ jaimax-1 PoA Chain Schema Created Successfully';
    RAISE NOTICE '   - Core tables: blocks, transactions, messages, events, address_transactions';
    RAISE NOTICE '   - State tables: indexer_state, validator_sync_state, proposal_sync_state, authority_sync_state';
    RAISE NOTICE '   - PoA tables: validators, validator_history, authority_accounts, authority_state, authority_actions';
    RAISE NOTICE '   - Gov tables: proposals, votes';
    RAISE NOTICE '   - Balance tables: balances';
    RAISE NOTICE '   - CosmWasm tables: wasm_contracts, wasm_codes, wasm_executions, wasm_instantiations, wasm_migrations';
    RAISE NOTICE '   - CosmWasm tables: wasm_events, cw20_transfers, bank_transfers';
    RAISE NOTICE '   - Views: validator_stats, authority_activity, chain_stats, active_authorities,';
    RAISE NOTICE '            contract_activity, recent_wasm_activity, cw20_address_activity';
    RAISE NOTICE '   - Functions: 8 utility functions';
    RAISE NOTICE '   - Triggers: 4 automatic triggers';
    RAISE NOTICE '';
    RAISE NOTICE 'Ready for jaimax-1 indexer!';
END $$; 
-- Check if indexer is fully synced with chain
CREATE OR REPLACE FUNCTION is_synced()
RETURNS BOOLEAN AS $$
DECLARE
    indexed_height BIGINT;
    chain_height   BIGINT;
BEGIN
    SELECT last_height INTO indexed_height
    FROM indexer_state
    WHERE id = 1;

    SELECT MAX(height) INTO chain_height
    FROM blocks;

    IF chain_height IS NULL THEN
        RETURN FALSE;
    END IF;

    RETURN indexed_height >= chain_height;
END;
$$ LANGUAGE plpgsql;

-- ============================================================================
-- TRIGGER: Auto-update balances on bank_transfer insert
-- Permanently keeps balances in sync with every native token transfer
-- No manual backfill needed on fresh deployments
-- ============================================================================

CREATE OR REPLACE FUNCTION sync_balance_on_bank_transfer()
RETURNS TRIGGER AS $$
BEGIN
    -- Credit the recipient
    INSERT INTO balances (address, denom, amount, height, updated_at)
    VALUES (NEW.to_address, NEW.denom, COALESCE(NEW.amount_value::TEXT, '0'), NEW.height, NOW())
    ON CONFLICT (address, denom) DO UPDATE SET
        amount = (CAST(balances.amount AS NUMERIC) + COALESCE(NEW.amount_value, 0))::TEXT,
        height = GREATEST(balances.height, NEW.height),
        updated_at = NOW();

    -- Debit the sender
    INSERT INTO balances (address, denom, amount, height, updated_at)
    VALUES (NEW.from_address, NEW.denom, '0', NEW.height, NOW())
    ON CONFLICT (address, denom) DO UPDATE SET
        amount = GREATEST(0, (CAST(balances.amount AS NUMERIC) - COALESCE(NEW.amount_value, 0)))::TEXT,
        height = GREATEST(balances.height, NEW.height),
        updated_at = NOW();

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trigger_sync_balance_on_bank_transfer ON bank_transfers;
CREATE TRIGGER trigger_sync_balance_on_bank_transfer
    AFTER INSERT ON bank_transfers
    FOR EACH ROW EXECUTE FUNCTION sync_balance_on_bank_transfer();


/* Authortity address is created 1 time thats why query and store by this command.

1.grpcurl -plaintext 192.168.0.147:9090 authority.v1.Query.Params

2.INSERT INTO authority_accounts (
  address,
  added_at,
  added_at_height,
  active
)
VALUES (
  'jaimax102rgn9gqfyklwhktc8uedrfyx97akx8nc4ej3r',
  NOW(),
  2100,
  true
)
ON CONFLICT (address) DO NOTHING;

3.SELECT * FROM authority_accounts;

*/