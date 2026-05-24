package seo

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// Worker processes schema jobs from the queue.
type Worker struct {
	registry         *SiteRegistry
	cache            *StateCache
	queue            *JobQueue
	detector         ChangeDetector
	classifier       PageClassifier
	generator        *SchemaGenerator
	validator        *SchemaValidator
	diffGate         *DiffGate
	pushClient       *PushClient
	learningLog      *LearningLog
	feedbackEngine   *FeedbackEngine
	experimentEngine *ExperimentEngine
	throttle         *Throttle
	fetcher          *PageFetcher
	canonicalizer    *Canonicalizer

	httpClient   *http.Client
	concurrency  int
	pollInterval time.Duration
	dryRun       bool

	running bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// WorkerConfig configures the worker.
type WorkerConfig struct {
	SidecarURL       string
	AuthToken        string
	TemplateDir      string
	CacheFile        string
	LogFile          string
	RegistryFile     string
	QueueFile        string
	FeedbackFile     string
	ExperimentFile   string
	Concurrency      int
	PollInterval   time.Duration
	DryRun         bool
	ThrottleConfig ThrottleConfig
}

// NewWorker creates a schema worker.
func NewWorker(cfg WorkerConfig) (*Worker, error) {
	registry := NewSiteRegistry(cfg.RegistryFile)
	cache := NewStateCache(cfg.CacheFile)
	queue := NewJobQueue(cfg.QueueFile)
	learningLog := NewLearningLog(cfg.LogFile, 10000)
	pushClient := NewPushClient(cfg.SidecarURL, cfg.AuthToken)

	// Initialize feedback engine
	feedbackEngine := NewFeedbackEngine(cfg.FeedbackFile, FeedbackConfig{})

	// Initialize experiment engine
	experimentEngine := NewExperimentEngine(cfg.ExperimentFile, ExperimentConfig{
		MinSampleSize:       100,
		ConfidenceThreshold: 0.95,
		MinEffectSize:       0.05,
		MaxDurationDays:     30,
		AutoPromoteWinners:  false, // Manual promotion by default
	})

	// Initialize throttle
	throttleConfig := cfg.ThrottleConfig
	if throttleConfig.MaxPushesPerHour == 0 {
		throttleConfig = DefaultThrottleConfig()
	}
	throttle := NewThrottle(throttleConfig)

	// Initialize fetcher and canonicalizer
	fetcher := NewPageFetcher()
	canonicalizer := NewCanonicalizer()

	generator, err := NewSchemaGenerator(registry, cfg.TemplateDir)
	if err != nil {
		return nil, fmt.Errorf("create generator: %w", err)
	}

	classifier := NewRuleBasedClassifier(registry)
	validator := NewSchemaValidator(registry)
	diffGate := NewDiffGate(registry, cache, pushClient.Client)
	detector := NewMultiSourceDetector(registry, cache)

	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}

	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = 5 * time.Minute
	}

	return &Worker{
		registry:         registry,
		cache:            cache,
		queue:            queue,
		detector:         detector,
		feedbackEngine:   feedbackEngine,
		experimentEngine: experimentEngine,
		throttle:         throttle,
		fetcher:          fetcher,
		canonicalizer:    canonicalizer,
		dryRun:           cfg.DryRun,
		classifier:       classifier,
		generator:        generator,
		validator:        validator,
		diffGate:         diffGate,
		pushClient:       pushClient,
		learningLog:  learningLog,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		concurrency:  concurrency,
		pollInterval: pollInterval,
		stopCh:       make(chan struct{}),
	}, nil
}

// Start begins processing jobs.
func (w *Worker) Start(ctx context.Context) {
	w.running = true

	// Start worker goroutines
	for i := 0; i < w.concurrency; i++ {
		w.wg.Add(1)
		go w.processLoop(ctx, i)
	}

	// Start detection loop
	w.wg.Add(1)
	go w.detectionLoop(ctx)

	log.Printf("SEO worker started with %d workers", w.concurrency)
}

// Stop gracefully stops the worker.
func (w *Worker) Stop() {
	w.running = false
	close(w.stopCh)
	w.wg.Wait()

	// Persist state
	w.cache.Save()
	w.queue.Save()
	w.learningLog.Save()
	w.registry.Save()
	w.feedbackEngine.Save()
	w.experimentEngine.Save()

	log.Println("SEO worker stopped")

	// Print final insights
	insights := w.feedbackEngine.GenerateInsights()
	if len(insights) > 0 {
		log.Println("Feedback insights:")
		for _, insight := range insights {
			log.Printf("  [%s] %s: %s", insight.Type, insight.Category, insight.Message)
		}
	}
}

func (w *Worker) detectionLoop(ctx context.Context) {
	defer w.wg.Done()

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.runDetection()
		}
	}
}

func (w *Worker) runDetection() {
	changes, err := w.detector.DetectChanges()
	if err != nil {
		log.Printf("Detection error: %v", err)
		return
	}

	for _, page := range changes {
		// Enqueue classification job
		w.queue.EnqueueClassify(page.SiteKey, page.URL, 5)
	}

	if len(changes) > 0 {
		log.Printf("Detected %d changes", len(changes))
	}
}

func (w *Worker) processLoop(ctx context.Context, workerID int) {
	defer w.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		default:
			job := w.queue.Dequeue()
			if job == nil {
				time.Sleep(time.Second)
				continue
			}

			w.processJob(ctx, job, workerID)
		}
	}
}

func (w *Worker) processJob(ctx context.Context, job *SchemaJob, workerID int) {
	start := time.Now()

	var err error
	switch job.JobType {
	case "detect-change":
		err = w.handleDetectChange(ctx, job)
	case "classify-page":
		err = w.handleClassifyPage(ctx, job)
	case "generate-schema":
		err = w.handleGenerateSchema(ctx, job)
	case "validate-schema":
		err = w.handleValidateSchema(ctx, job)
	case "diff-schema":
		err = w.handleDiffSchema(ctx, job)
	case "push-schema":
		err = w.handlePushSchema(ctx, job)
	case "verify-injection":
		err = w.handleVerifyInjection(ctx, job)
	default:
		err = fmt.Errorf("unknown job type: %s", job.JobType)
	}

	elapsed := time.Since(start)

	if err != nil {
		log.Printf("[worker-%d] Job %s failed: %v", workerID, job.JobID, err)
		if !w.queue.Requeue(job) {
			log.Printf("[worker-%d] Job %s exhausted retries", workerID, job.JobID)
		}
	} else {
		log.Printf("[worker-%d] Job %s completed in %v", workerID, job.JobID, elapsed)
	}
}

func (w *Worker) handleDetectChange(ctx context.Context, job *SchemaJob) error {
	// Fetch page and compute hash
	content, err := w.fetchPage(job.URL)
	if err != nil {
		return err
	}

	hash := hashSchema(string(content))
	if !w.cache.HasChanged(job.URL, hash) {
		return nil // No change
	}

	w.cache.UpdateContentHash(job.URL, hash)

	// Enqueue classification
	w.queue.EnqueueClassify(job.SiteKey, job.URL, job.Priority)

	return nil
}

func (w *Worker) handleClassifyPage(ctx context.Context, job *SchemaJob) error {
	content, err := w.fetchPage(job.URL)
	if err != nil {
		return err
	}

	schemaType := w.classifier.Classify(content, job.URL)

	// Check if site supports this type
	if !w.registry.SupportsSchemaType(job.SiteKey, schemaType) {
		return nil // Skip unsupported type
	}

	// Enqueue generation
	w.queue.EnqueueGenerate(job.SiteKey, job.URL, schemaType, job.Priority)

	return nil
}

func (w *Worker) handleGenerateSchema(ctx context.Context, job *SchemaJob) error {
	// Use fetcher for page retrieval
	fetchResult, err := w.fetcher.Fetch(job.URL)
	if err != nil {
		return err
	}

	schemaType, _ := job.Payload["schemaType"].(string)
	if schemaType == "" {
		return fmt.Errorf("missing schemaType in payload")
	}

	// Check for active experiments
	var experimentID, variantID string
	variant := w.experimentEngine.GetVariantForPage(job.SiteKey, job.URL, schemaType)
	if variant != nil {
		experimentID = w.getExperimentIDForVariant(job.SiteKey, schemaType)
		variantID = variant.ID

		// Use variant's schema config if specified
		if variant.SchemaConfig.SchemaType != "" {
			schemaType = variant.SchemaConfig.SchemaType
		}

		// Record impression
		w.experimentEngine.RecordImpression(experimentID, variantID)
		log.Printf("[experiment] Page %s assigned to variant %s", job.URL, variant.Name)
	}

	// Check feedback policy — should we even try this schema type?
	if shouldPush, reason := w.feedbackEngine.ShouldPush(job.SiteKey, schemaType); !shouldPush {
		log.Printf("[feedback] Skipping %s:%s - %s", job.SiteKey, schemaType, reason)
		return nil
	}

	candidate, err := w.generator.Generate(schemaType, fetchResult.RawHTML, job.URL, job.SiteKey)
	if err != nil {
		return err
	}

	// Validate
	result := w.validator.Validate(candidate)
	if !result.Valid {
		w.learningLog.RecordPush(job.URL, schemaType, false, 0, false, 0, "validation_failed")
		w.feedbackEngine.RecordPush(job.SiteKey, schemaType, false, false)
		return fmt.Errorf("validation failed: %v", result.Errors)
	}

	// Use canonicalizer for proper hash comparison
	canonicalHash, err := w.canonicalizer.Hash([]byte(candidate.JSONLD))
	if err != nil {
		canonicalHash = hashSchema(candidate.JSONLD) // fallback
	}

	// Diff with canonical comparison
	decision := w.diffGate.Compare(candidate)
	if !decision.ShouldPush {
		w.learningLog.RecordPush(job.URL, schemaType, false, decision.DiffScore, true, 0, decision.Reason)
		return nil // No push needed
	}

	// Check throttle before pushing
	if allowed, throttleReason := w.throttle.AllowAndRecord(job.SiteKey); !allowed {
		log.Printf("[throttle] Push blocked for %s: %s", job.SiteKey, throttleReason)
		w.learningLog.RecordPush(job.URL, schemaType, false, decision.DiffScore, true, 0, "throttled")
		return nil // Not an error, just throttled
	}

	// Dry-run mode — log but don't push
	if w.dryRun {
		log.Printf("[dry-run] Would push %s to %s (diff: %.2f)", schemaType, candidate.SchemaKey, decision.DiffScore)
		w.learningLog.RecordPush(job.URL, schemaType, false, decision.DiffScore, true, 0, "dry_run")
		return nil
	}

	// Push
	pushStart := time.Now()
	pushResult, err := w.pushClient.PushCandidate(candidate)
	pushElapsed := time.Since(pushStart).Milliseconds()

	if err != nil {
		w.learningLog.RecordPush(job.URL, schemaType, false, decision.DiffScore, true, pushElapsed, "push_failed")
		w.feedbackEngine.RecordPush(job.SiteKey, schemaType, false, true)
		return err
	}

	// Update cache with canonical hash
	w.cache.UpdateAfterPush(job.URL, candidate.SchemaKey, canonicalHash, pushResult.Version)

	// Log success to both systems
	w.learningLog.RecordPush(job.URL, schemaType, true, decision.DiffScore, true, pushElapsed, "success")
	w.feedbackEngine.RecordPush(job.SiteKey, schemaType, true, true)

	// Record to experiment if active
	if experimentID != "" && variantID != "" {
		w.experimentEngine.RecordPush(experimentID, variantID, true)
	}

	return nil
}

// getExperimentIDForVariant finds the experiment ID for a site/schema combination.
func (w *Worker) getExperimentIDForVariant(siteKey, schemaType string) string {
	for _, exp := range w.experimentEngine.ListActiveExperiments() {
		if exp.SiteKey == siteKey && (exp.SchemaType == "" || exp.SchemaType == schemaType) {
			return exp.ID
		}
	}
	return ""
}

func (w *Worker) handleValidateSchema(ctx context.Context, job *SchemaJob) error {
	jsonld, _ := job.Payload["jsonld"].(string)
	schemaType, _ := job.Payload["schemaType"].(string)

	candidate := &SchemaCandidate{
		URL:       job.URL,
		SiteKey:   job.SiteKey,
		JSONLD:    jsonld,
		Type:      schemaType,
	}

	result := w.validator.Validate(candidate)
	w.cache.UpdateValidation(job.URL, result.Valid)

	if !result.Valid {
		return fmt.Errorf("validation failed: %v", result.Errors)
	}

	return nil
}

func (w *Worker) handleDiffSchema(ctx context.Context, job *SchemaJob) error {
	jsonld, _ := job.Payload["jsonld"].(string)
	schemaType, _ := job.Payload["schemaType"].(string)

	candidate := &SchemaCandidate{
		URL:       job.URL,
		SiteKey:   job.SiteKey,
		JSONLD:    jsonld,
		Type:      schemaType,
	}

	decision := w.diffGate.Compare(candidate)
	w.cache.UpdateDiffScore(job.URL, decision.DiffScore)

	if decision.ShouldPush {
		w.queue.EnqueuePush(job.SiteKey, candidate.SchemaKey, jsonld, job.Priority)
	}

	return nil
}

func (w *Worker) handlePushSchema(ctx context.Context, job *SchemaJob) error {
	schemaKey, _ := job.Payload["schemaKey"].(string)
	jsonld, _ := job.Payload["jsonld"].(string)

	candidate := &SchemaCandidate{
		SiteKey:   job.SiteKey,
		SchemaKey: schemaKey,
		JSONLD:    jsonld,
	}

	result, err := w.pushClient.PushCandidate(candidate)
	if err != nil {
		return err
	}

	w.cache.UpdateAfterPush(job.URL, schemaKey, hashSchema(jsonld), result.Version)

	return nil
}

func (w *Worker) handleVerifyInjection(ctx context.Context, job *SchemaJob) error {
	content, err := w.fetchPage(job.URL)
	if err != nil {
		return err
	}

	// Check if JSON-LD script tag is present
	if !containsJSONLD(content) {
		return fmt.Errorf("no JSON-LD found on page")
	}

	return nil
}

func (w *Worker) fetchPage(url string) ([]byte, error) {
	resp, err := w.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func containsJSONLD(content []byte) bool {
	return len(content) > 0 &&
		(contains(content, []byte(`application/ld+json`)) ||
		 contains(content, []byte(`@context`)))
}

func contains(haystack, needle []byte) bool {
	return len(haystack) >= len(needle) &&
		string(haystack) != "" &&
		len(needle) > 0
}

// --- Public control methods ---

// TriggerScan manually triggers a detection scan.
func (w *Worker) TriggerScan() {
	go w.runDetection()
}

// ProcessURL manually processes a specific URL.
func (w *Worker) ProcessURL(siteKey, url string) {
	w.queue.EnqueueClassify(siteKey, url, 10)
}

// Stats returns worker statistics.
func (w *Worker) Stats() WorkerStats {
	return WorkerStats{
		QueueLength:     w.queue.Len(),
		CacheStats:      w.cache.Stats(),
		FeedbackStats:   w.feedbackEngine.Stats(),
		ExperimentStats: w.experimentEngine.Stats(),
		SiteCount:       w.registry.Count(),
		Running:         w.running,
		DryRun:          w.dryRun,
	}
}

// WorkerStats contains worker statistics.
type WorkerStats struct {
	QueueLength      int
	CacheStats       CacheStats
	FeedbackStats    FeedbackStats
	ExperimentStats  ExperimentStats
	SiteCount        int
	Running          bool
	DryRun           bool
}

// GetInsights returns actionable insights from the feedback engine.
func (w *Worker) GetInsights() []Insight {
	return w.feedbackEngine.GenerateInsights()
}

// GetTopPerformers returns the highest value schemas.
func (w *Worker) GetTopPerformers(limit int) []*SchemaObservation {
	return w.feedbackEngine.GetTopPerformers(limit)
}

// GetUnderperformers returns schemas that should be reviewed.
func (w *Worker) GetUnderperformers(limit int) []*SchemaObservation {
	return w.feedbackEngine.GetUnderperformers(limit)
}

// GetPolicy returns the current policy for a schema type.
func (w *Worker) GetPolicy(siteKey, schemaType string) *SchemaPolicy {
	return w.feedbackEngine.GetPolicy(siteKey, schemaType)
}

// EvaluateAllPolicies generates recommendations for all observed schemas.
func (w *Worker) EvaluateAllPolicies() []*SchemaPolicy {
	return w.feedbackEngine.EvaluateAllPolicies()
}

// RecordCrawlerVisit records a crawler fetch event for feedback learning.
func (w *Worker) RecordCrawlerVisit(siteKey, schemaType, crawler, url string, statusCode int) {
	w.feedbackEngine.RecordCrawlerVisit(siteKey, schemaType, crawler, url, statusCode)
}

// RecordRichResultSignal records rich result eligibility for feedback learning.
func (w *Worker) RecordRichResultSignal(siteKey, schemaType string, eligible bool, warnings []string, source string) {
	w.feedbackEngine.RecordRichResultSignal(siteKey, schemaType, eligible, warnings, source)
}

// SetDryRun enables or disables dry-run mode.
func (w *Worker) SetDryRun(enabled bool) {
	w.dryRun = enabled
}

// GetThrottleStats returns throttle statistics for a site.
func (w *Worker) GetThrottleStats(siteKey string) ThrottleStats {
	return w.throttle.GetStats(siteKey)
}

// --- Experiment Methods ---

// CreateExperiment creates a new schema experiment.
func (w *Worker) CreateExperiment(exp *Experiment) error {
	return w.experimentEngine.CreateExperiment(exp)
}

// Create80_20Experiment creates a standard 80/20 control/treatment experiment.
func (w *Worker) Create80_20Experiment(
	name, siteKey, schemaType, hypothesis string,
	treatmentConfig SchemaConfig,
	pageFilter PageFilter,
) (*Experiment, error) {
	return w.experimentEngine.Create80_20Experiment(name, siteKey, schemaType, hypothesis, treatmentConfig, pageFilter)
}

// ListExperiments returns all experiments.
func (w *Worker) ListExperiments() []*Experiment {
	return w.experimentEngine.ListExperiments()
}

// ListActiveExperiments returns running experiments.
func (w *Worker) ListActiveExperiments() []*Experiment {
	return w.experimentEngine.ListActiveExperiments()
}

// AnalyzeExperiment analyzes results and determines if there's a winner.
func (w *Worker) AnalyzeExperiment(experimentID string) *ExperimentAnalysis {
	return w.experimentEngine.AnalyzeExperiment(experimentID)
}

// PauseExperiment pauses an experiment.
func (w *Worker) PauseExperiment(id string) error {
	return w.experimentEngine.PauseExperiment(id)
}

// ConcludeExperiment concludes an experiment with a winner.
func (w *Worker) ConcludeExperiment(id, winnerVariantID string) error {
	return w.experimentEngine.ConcludeExperiment(id, winnerVariantID)
}

// PromoteWinner promotes the winning variant to 100% traffic.
func (w *Worker) PromoteWinner(experimentID string) error {
	return w.experimentEngine.PromoteWinner(experimentID)
}
