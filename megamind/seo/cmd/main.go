package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"megamind/seo"
)

func main() {
	// Command line flags
	sidecarURL := flag.String("sidecar", "http://<SIDECAR_HOST>:9090", "Sidecar URL")
	authToken := flag.String("token", "", "Auth token for sidecar")
	templateDir := flag.String("templates", "./templates", "Template directory")
	dataDir := flag.String("data", "./data", "Data directory for cache/logs")
	concurrency := flag.Int("workers", 4, "Number of worker goroutines")
	dryRun := flag.Bool("dry-run", false, "Run in dry-run mode (no pushes)")
	pollInterval := flag.Duration("poll", 5*time.Minute, "Detection poll interval")
	maxPushesPerHour := flag.Int("max-pushes", 10, "Max pushes per site per hour")
	flag.Parse()

	if *authToken == "" {
		*authToken = os.Getenv("SEO_AUTH_TOKEN")
	}
	if *authToken == "" {
		log.Fatal("Auth token required (--token or SEO_AUTH_TOKEN)")
	}

	// Ensure data directory exists
	os.MkdirAll(*dataDir, 0755)

	// Create worker
	worker, err := seo.NewWorker(seo.WorkerConfig{
		SidecarURL:   *sidecarURL,
		AuthToken:    *authToken,
		TemplateDir:  *templateDir,
		CacheFile:    *dataDir + "/cache.json",
		LogFile:      *dataDir + "/learning.json",
		RegistryFile: *dataDir + "/registry.json",
		QueueFile:    *dataDir + "/queue.json",
		FeedbackFile:   *dataDir + "/feedback.json",
		ExperimentFile: *dataDir + "/experiments.json",
		Concurrency:  *concurrency,
		PollInterval: *pollInterval,
		DryRun:       *dryRun,
		ThrottleConfig: seo.ThrottleConfig{
			MaxPushesPerHour:   *maxPushesPerHour,
			MinIntervalBetween: 5 * time.Minute,
			BurstAllowance:     3,
			BurstRecoveryMinutes: 20,
		},
	})
	if err != nil {
		log.Fatalf("Failed to create worker: %v", err)
	}

	if *dryRun {
		log.Println("Running in DRY-RUN mode - no schemas will be pushed")
	}

	// Load default site configurations
	// In production, these would come from a config file or database
	worker.TriggerScan() // Initial scan

	// Start worker
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	cancel()
	worker.Stop()
}
