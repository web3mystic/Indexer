# Blockchain Indexer

A production-ready, high-performance blockchain indexer for jaimax SDK chains. This indexer follows enterprise-grade architecture with clean separation of concerns.

## 📋 Table of Contents

- [Architecture](#architecture)
- [Features](#features)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Configuration](#configuration)
- [Database Setup](#database-setup)
- [Running the Indexer](#running-the-indexer)
- [Project Structure](#project-structure)
- [API Queries](#api-queries)
- [Monitoring](#monitoring)
- [Troubleshooting](#troubleshooting)

---

## 🏗 Architecture

The indexer follows a **3-layer architecture** for maximum scalability and maintainability:

```
┌─────────────────────────────────────────────┐
│          jaimax NODE (gRPC)                 │
└─────────────────┬───────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────┐
│         FETCHER LAYER                       │
│  • gRPC client                              │
│  • Block/transaction retrieval              │
│  • Connection management                    │
└─────────────────┬───────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────┐
│         PARSER LAYER                        │
│  • Message decoding                         │
│  • Event extraction                         │
│  • Address extraction                       │
└─────────────────┬───────────────────────────┘
                  │
                  ▼
┌─────────────────────────────────────────────┐
│         STORAGE LAYER                       │
│  • PostgreSQL persistence                   │
│  • Atomic transactions                      │
│  • Indexer state tracking                   │
└─────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility | No Access To |
|-----------|----------------|--------------|
| **Fetcher** | Communicate with blockchain via gRPC | Database, Business Logic |
| **Parser** | Transform raw data into structured format | gRPC, Database |
| **Storage** | Persist data atomically | gRPC, Business Logic |
| **Coordinator** | Orchestrate the pipeline | Nothing (uses all components) |

---

## ✨ Features

**Production-Ready Architecture**
- Clean separation of concerns
- Interface-based design
- Easy testing and mocking

**Robust Error Handling**
- Automatic retry with exponential backoff
- Resume from last indexed height
- Transaction atomicity

**High Performance**
- Efficient batch processing
- Connection pooling
- Optimized database queries

**Complete Data Extraction**
- Block metadata
- Transaction details
- Decoded messages
- Events and attributes
- Address mappings

**Developer Friendly**
- Clear logging
- Progress tracking
- Environment-based configuration

---

## 📦 Prerequisites

Before you begin, ensure you have:

- **Go 1.21+** installed
- **PostgreSQL 14+** running
- **Access to a jaimax node** with gRPC enabled
- **Git** for version control

---

## 🔧 Installation

### 1. Clone the Repository

```bash
git clone https://github.com/yourusername/jaimax-indexer
cd jaimax-indexer
```

### 2. Install Dependencies

```bash
go mod download
```

### 3. Build the Indexer

```bash
go build -o indexer ./cmd/indexer
```

---

## ⚙️ Configuration

### Environment Variables

Copy the example configuration:

```bash
cp .env.example .env
```

Edit `.env` with your settings:

```bash
# Blockchain
GRPC_ENDPOINT=localhost:9090
CHAIN_ID=jaimaxhub-4

# Database
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=yourpassword
DB_NAME=jaimax_indexer

# Indexer
START_HEIGHT=1
BATCH_SIZE=100
WORKER_COUNT=4
RETRY_ATTEMPTS=3
RETRY_DELAY_MS=1000
```

### Configuration Options

| Variable | Description | Default |
|----------|-------------|---------|
| `GRPC_ENDPOINT` | gRPC endpoint of jaimax node | `localhost:9090` |
| `CHAIN_ID` | Chain identifier | `jaimaxhub-4` |
| `DB_HOST` | PostgreSQL host | `localhost` |
| `DB_PORT` | PostgreSQL port | `5432` |
| `DB_NAME` | Database name | `jaimax_indexer` |
| `START_HEIGHT` | Starting block height | `1` |
| `BATCH_SIZE` | Blocks per batch | `100` |
| `WORKER_COUNT` | Concurrent workers | `4` |
| `RETRY_ATTEMPTS` | Max retry attempts | `3` |
| `RETRY_DELAY_MS` | Retry delay (ms) | `1000` |

---

## 💾 Database Setup

### 1. Create Database

```bash
createdb jaimax_indexer
```

Or via psql:

```sql
CREATE DATABASE jaimax_indexer;
```

### 2. Run Migrations

```bash
psql -U postgres -d jaimax_indexer -f migrations/001_initial_schema.sql
```

### 3. Verify Tables

```bash
psql -U postgres -d jaimax_indexer -c "\dt"
```

You should see:
- `blocks`
- `transactions`
- `messages`
- `events`
- `address_transactions`
- `indexer_state`

---
# run migration of schema to your db
psql -U test -d jaimax_v6 -f migrations/001_jaimax_schema.sql


## Running the Indexer

### Start Indexing

```bash
# From source
go run ./cmd/indexer/main.go

# From binary
./indexer
```

### Expected Output

```
╔════════════════════════════════════════════╗
║     jaimax Blockchain Indexer v1.0.0      ║
╚════════════════════════════════════════════╝

⚙️  Configuration:
   - Chain ID: jaimaxhub-4
   - gRPC Endpoint: localhost:9090
   - Database: jaimax_indexer
   - Start Height: 1
   - Batch Size: 100

🔌 Connecting to blockchain...
Connected to gRPC endpoint
🧠 Initializing parser...
Parser ready
💾 Connecting to database...
Database connected
🎯 Initializing coordinator...
Coordinator ready

═══════════════════════════════════════════
        Starting Indexer Pipeline
═══════════════════════════════════════════

Starting jaimax Indexer...
📍 Starting from height 1
Chain height: 18500000
✓ Indexed block 1/18500000 (0.00%)
✓ Indexed block 2/18500000 (0.00%)
...
```

### Resume from Last Height

The indexer automatically resumes from the last successfully indexed block:

```
📍 Resuming from height 150000
```

### Graceful Shutdown

Press `Ctrl+C` to stop:

```
^C
🛑 Shutdown signal received, stopping gracefully...
Indexer shutdown complete
```

---

## 📁 Project Structure

```
jaimax-indexer/
├── cmd/
│   └── indexer/
│       └── main.go              # Application entry point
├── internal/
│   ├── fetcher/
│   │   └── fetcher.go           # Blockchain data fetcher
│   ├── parser/
│   │   └── parser.go            # Transaction parser
│   ├── storage/
│   │   └── storage.go           # PostgreSQL storage
│   └── coordinator/
│       └── coordinator.go       # Pipeline orchestrator
├── pkg/
│   ├── config/
│   │   └── config.go            # Configuration management
│   └── types/
│       └── types.go             # Shared data types
├── migrations/
│   └── 001_initial_schema.sql   # Database schema
├── go.mod                        # Go dependencies
├── go.sum                        # Dependency checksums
├── .env.example                  # Configuration template
└── README.md                     # This file
```

---

## 🔍 API Queries

### Get Recent Transactions

```sql
SELECT * FROM recent_transactions LIMIT 10;
```

### Get Transactions by Address

```sql
SELECT t.*
FROM transactions t
JOIN address_transactions at ON t.hash = at.tx_hash
WHERE at.address = 'jaimax1...'
ORDER BY t.height DESC;
```

### Get Block Information

```sql
SELECT * FROM blocks WHERE height = 1000000;
```

### Get Transaction Count by Address

```sql
SELECT get_tx_count_by_address('jaimax1...');
```

### Get Address Activity Summary

```sql
SELECT * FROM address_activity
WHERE address = 'jaimax1...';
```

### Get Failed Transactions

```sql
SELECT * FROM transactions
WHERE success = false
ORDER BY height DESC
LIMIT 100;
```

### Get Transactions by Message Type

```sql
SELECT * FROM transactions
WHERE msg_types @> '["MsgSend"]'::jsonb
ORDER BY height DESC;
```

---

## Monitoring

### Check Indexer Status

Query the indexer state:

```sql
SELECT * FROM indexer_state;
```

Returns:
```
 id | last_height | last_block_hash | updated_at
----+-------------+-----------------+------------
  1 |      150000 | ABC123...       | 2024-01-15
```

### Check Sync Status

```sql
SELECT is_synced();
```

Returns `true` if within 10 blocks of chain tip.

### Monitor Performance

```sql
-- Blocks indexed per hour
SELECT 
    DATE_TRUNC('hour', created_at) as hour,
    COUNT(*) as blocks_indexed
FROM blocks
GROUP BY hour
ORDER BY hour DESC
LIMIT 24;
```

---

## 🐛 Troubleshooting

### Problem: gRPC Connection Failed

**Error:**
```
❌ Failed to create fetcher: failed to connect to gRPC endpoint
```

**Solution:**
- Verify node is running: `curl http://localhost:9090`
- Check firewall settings
- Ensure gRPC is enabled in node config

---

### Problem: Database Connection Failed

**Error:**
```
❌ Failed to create storage: failed to ping database
```

**Solution:**
```bash
# Check PostgreSQL is running
systemctl status postgresql

# Test connection
psql -U postgres -c "SELECT version();"
```

---

### Problem: Out of Memory

**Solution:**
- Reduce `BATCH_SIZE`
- Lower `WORKER_COUNT`
- Increase system RAM

---

### Problem: Slow Indexing

**Solutions:**
1. **Add database indexes:**
```sql
CREATE INDEX CONCURRENTLY idx_custom ON transactions(your_column);
```

2. **Tune PostgreSQL:**
```sql
ALTER SYSTEM SET shared_buffers = '2GB';
ALTER SYSTEM SET effective_cache_size = '6GB';
```

3. **Use SSD storage**

---

## 🧪 Testing

### Run Unit Tests

```bash
go test ./...
```

### Test Specific Component

```bash
go test ./internal/parser -v
```

### Integration Test

```bash
# Set test environment
export GRPC_ENDPOINT=testnet:9090
export DB_NAME=jaimax_indexer_test

# Run indexer
go run ./cmd/indexer/main.go
```

---

## 🔒 Production Deployment

### Docker Deployment

Create `Dockerfile`:

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o indexer ./cmd/indexer

FROM alpine:latest
RUN apk --no-cache add ca-certificates
COPY --from=builder /app/indexer /indexer
CMD ["/indexer"]
```

Build and run:

```bash
docker build -t jaimax-indexer .
docker run --env-file .env jaimax-indexer
```

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: jaimax-indexer
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: indexer
        image: jaimax-indexer:latest
        envFrom:
        - configMapRef:
            name: indexer-config
```

---

## 📈 Performance Benchmarks

Typical performance on modern hardware:

| Metric | Value |
|--------|-------|
| Blocks/second | 50-100 |
| Transactions/second | 500-1000 |
| Database writes/second | 2000-5000 |
| Memory usage | 500MB-2GB |

---

## 🤝 Contributing

Contributions are welcome! Please:

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

---

## 📄 License

MIT License - see LICENSE file for details

---

## 🙏 Acknowledgments

Built with:
- [jaimax SDK](https://github.com/jaimax/jaimax-sdk)
- [PostgreSQL](https://www.postgresql.org/)
- [gRPC](https://grpc.io/)

---

## 📞 Support

For issues and questions:
- GitHub Issues: [github.com/yourusername/jaimax-indexer/issues]
- Documentation: [docs.yoursite.com]
- Discord: [discord.gg/yourserver]

---

**Happy Indexing! 🚀**
