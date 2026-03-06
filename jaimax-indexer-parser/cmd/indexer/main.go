package main

// Loads environment configuration
// Connects to the Cosmos chain via gRPC
// Initializes the parser
// Connects to PostgreSQL
// Creates a coordinator
// Handles graceful shutdown
// Starts the indexing pipeline
import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/cosmos-indexer/internal/coordinator"
	"github.com/cosmos-indexer/internal/fetcher"
	"github.com/cosmos-indexer/internal/parser"
	"github.com/cosmos-indexer/internal/storage"
	"github.com/cosmos-indexer/pkg/config"
)

func main() {
	// Load configuration
	godotenv.Load()
	cfg, err := config.LoadFromEnv()
	if err != nil {
		fmt.Printf(" Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("╔════════════════════════════════════════════╗")
	fmt.Println("║     Jaimax Blockchain Indexer v1.0.0      ║")
	fmt.Println("╚════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("	Configuration:\n")
	fmt.Printf("   - Chain ID: %s\n", cfg.ChainID)
	fmt.Printf("   - gRPC Endpoint: %s\n", cfg.GRPCEndpoint)
	fmt.Printf("   - Database: %s\n", cfg.DBName)
	fmt.Printf("   - Start Height: %d\n", cfg.StartHeight)
	fmt.Printf("   - Batch Size: %d\n", cfg.BatchSize)
	fmt.Println()

	// Initialize components
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Initialize Fetcher
	fmt.Println("Connecting to blockchain...")
	fetch, err := fetcher.NewGRPCFetcher(cfg.GRPCEndpoint)
	if err != nil {
		fmt.Printf(" Failed to create fetcher: %v\n", err)
		os.Exit(1)
	}
	defer fetch.Close()
	fmt.Println("Connected to gRPC endpoint")

	// 2. Initialize Parser
	fmt.Println(" Initializing parser...")
	parse := parser.NewCosmosParser(cfg.ChainID)
	fmt.Println("Parser ready")

	// 3. Initialize Storage
	fmt.Println(" Connecting to database...")
	store, err := storage.NewPostgresStorage(cfg.ConnectionString())
	if err != nil {
		fmt.Printf(" Failed to create storage: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()
	fmt.Println("Database connected")

	// 4. Initialize Coordinator
	fmt.Println(" Initializing coordinator...")
	coord := coordinator.NewCoordinator(fetch, parse, store, cfg)
	fmt.Println("Coordinator ready")
	fmt.Println()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\n Shutdown signal received, stopping gracefully...")
		cancel()
	}()

	// Start indexing
	fmt.Println("═══════════════════════════════════════════")
	fmt.Println("        Starting Indexer Pipeline")
	fmt.Println("═══════════════════════════════════════════")
	fmt.Println()

	if err := coord.Start(ctx); err != nil && err != context.Canceled {
		fmt.Printf(" Indexer error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\nIndexer shutdown complete")
}
