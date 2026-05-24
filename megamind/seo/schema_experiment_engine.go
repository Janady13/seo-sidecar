package seo

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sync"
	"time"
)

// ExperimentEngine runs controlled schema experiments.
type ExperimentEngine struct {
	mu          sync.RWMutex
	experiments map[string]*Experiment
	results     map[string]*ExperimentResults
	filePath    string
	config      ExperimentConfig
}

// ExperimentConfig configures the experiment engine.
type ExperimentConfig struct {
	MinSampleSize        int           // Minimum observations before analysis
	ConfidenceThreshold  float64       // Required confidence for winner (0-1)
	MinEffectSize        float64       // Minimum improvement to declare winner
	MaxDurationDays      int           // Auto-conclude after this many days
	AutoPromoteWinners   bool          // Automatically promote winning variants
}

// Experiment defines a schema experiment.
type Experiment struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	SiteKey         string           `json:"site_key"`
	SchemaType      string           `json:"schema_type"`
	Status          string           `json:"status"` // running, paused, concluded
	StartedAt       time.Time        `json:"started_at"`
	ConcludedAt     *time.Time       `json:"concluded_at,omitempty"`
	Variants        []ExperimentVariant `json:"variants"`
	WinnerVariantID string           `json:"winner_variant_id,omitempty"`
	Hypothesis      string           `json:"hypothesis"`
	PageFilter      PageFilter       `json:"page_filter"`
}

// ExperimentVariant defines a variant in an experiment.
type ExperimentVariant struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	TrafficPercent int            `json:"traffic_percent"` // 0-100
	SchemaConfig   SchemaConfig   `json:"schema_config"`
	IsControl      bool           `json:"is_control"`
}

// SchemaConfig defines how to generate schema for a variant.
type SchemaConfig struct {
	SchemaType     string            `json:"schema_type"`
	TemplateOverride string          `json:"template_override,omitempty"`
	FieldOverrides map[string]any    `json:"field_overrides,omitempty"`
	Enrichments    []string          `json:"enrichments,omitempty"` // e.g., ["add_faq", "add_breadcrumb"]
}

// PageFilter determines which pages are included in an experiment.
type PageFilter struct {
	PathPatterns    []string `json:"path_patterns,omitempty"`
	ExcludePatterns []string `json:"exclude_patterns,omitempty"`
	PageTypes       []string `json:"page_types,omitempty"`
	SamplePercent   int      `json:"sample_percent"` // What % of matching pages to include
}

// ExperimentResults tracks outcomes for an experiment.
type ExperimentResults struct {
	ExperimentID    string                     `json:"experiment_id"`
	VariantResults  map[string]*VariantResults `json:"variant_results"`
	LastAnalyzed    time.Time                  `json:"last_analyzed"`
	CurrentWinner   string                     `json:"current_winner,omitempty"`
	Confidence      float64                    `json:"confidence"`
	Recommendation  string                     `json:"recommendation"`
}

// VariantResults tracks outcomes for a single variant.
type VariantResults struct {
	VariantID          string    `json:"variant_id"`
	Impressions        int       `json:"impressions"`
	Pushes             int       `json:"pushes"`
	ValidationSuccess  int       `json:"validation_success"`
	ValidationFailure  int       `json:"validation_failure"`
	CrawlerVisits      int       `json:"crawler_visits"`
	RichResultEligible int       `json:"rich_result_eligible"`
	TotalEffectiveness float64   `json:"total_effectiveness"`
	AvgEffectiveness   float64   `json:"avg_effectiveness"`
	LastObservation    time.Time `json:"last_observation"`
}

// NewExperimentEngine creates an experiment engine.
func NewExperimentEngine(filePath string, config ExperimentConfig) *ExperimentEngine {
	if config.MinSampleSize == 0 {
		config.MinSampleSize = 100
	}
	if config.ConfidenceThreshold == 0 {
		config.ConfidenceThreshold = 0.95
	}
	if config.MinEffectSize == 0 {
		config.MinEffectSize = 0.05 // 5% improvement
	}
	if config.MaxDurationDays == 0 {
		config.MaxDurationDays = 30
	}

	engine := &ExperimentEngine{
		experiments: make(map[string]*Experiment),
		results:     make(map[string]*ExperimentResults),
		filePath:    filePath,
		config:      config,
	}

	engine.load()

	return engine
}

// --- Experiment Management ---

// CreateExperiment creates a new experiment.
func (e *ExperimentEngine) CreateExperiment(exp *Experiment) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if exp.ID == "" {
		exp.ID = fmt.Sprintf("exp_%d", time.Now().UnixNano())
	}
	if exp.Status == "" {
		exp.Status = "running"
	}
	if exp.StartedAt.IsZero() {
		exp.StartedAt = time.Now()
	}

	// Validate traffic percentages sum to 100
	totalTraffic := 0
	for _, v := range exp.Variants {
		totalTraffic += v.TrafficPercent
	}
	if totalTraffic != 100 {
		return fmt.Errorf("variant traffic percentages must sum to 100, got %d", totalTraffic)
	}

	e.experiments[exp.ID] = exp
	e.results[exp.ID] = &ExperimentResults{
		ExperimentID:   exp.ID,
		VariantResults: make(map[string]*VariantResults),
	}

	for _, v := range exp.Variants {
		e.results[exp.ID].VariantResults[v.ID] = &VariantResults{
			VariantID: v.ID,
		}
	}

	return nil
}

// Create80_20Experiment creates a standard 80/20 control/treatment experiment.
func (e *ExperimentEngine) Create80_20Experiment(
	name, siteKey, schemaType, hypothesis string,
	treatmentConfig SchemaConfig,
	pageFilter PageFilter,
) (*Experiment, error) {
	exp := &Experiment{
		Name:       name,
		SiteKey:    siteKey,
		SchemaType: schemaType,
		Hypothesis: hypothesis,
		PageFilter: pageFilter,
		Variants: []ExperimentVariant{
			{
				ID:             "control",
				Name:           "Control (Standard)",
				TrafficPercent: 80,
				IsControl:      true,
				SchemaConfig: SchemaConfig{
					SchemaType: schemaType,
				},
			},
			{
				ID:             "treatment",
				Name:           "Treatment (Enhanced)",
				TrafficPercent: 20,
				IsControl:      false,
				SchemaConfig:   treatmentConfig,
			},
		},
	}

	if err := e.CreateExperiment(exp); err != nil {
		return nil, err
	}

	return exp, nil
}

// Create50_50Experiment creates an even split experiment.
func (e *ExperimentEngine) Create50_50Experiment(
	name, siteKey, schemaType, hypothesis string,
	controlConfig, treatmentConfig SchemaConfig,
	pageFilter PageFilter,
) (*Experiment, error) {
	exp := &Experiment{
		Name:       name,
		SiteKey:    siteKey,
		SchemaType: schemaType,
		Hypothesis: hypothesis,
		PageFilter: pageFilter,
		Variants: []ExperimentVariant{
			{
				ID:             "control",
				Name:           "Control",
				TrafficPercent: 50,
				IsControl:      true,
				SchemaConfig:   controlConfig,
			},
			{
				ID:             "treatment",
				Name:           "Treatment",
				TrafficPercent: 50,
				IsControl:      false,
				SchemaConfig:   treatmentConfig,
			},
		},
	}

	if err := e.CreateExperiment(exp); err != nil {
		return nil, err
	}

	return exp, nil
}

// GetExperiment returns an experiment by ID.
func (e *ExperimentEngine) GetExperiment(id string) *Experiment {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if exp, ok := e.experiments[id]; ok {
		copy := *exp
		return &copy
	}
	return nil
}

// ListExperiments returns all experiments.
func (e *ExperimentEngine) ListExperiments() []*Experiment {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*Experiment, 0, len(e.experiments))
	for _, exp := range e.experiments {
		copy := *exp
		result = append(result, &copy)
	}
	return result
}

// ListActiveExperiments returns running experiments.
func (e *ExperimentEngine) ListActiveExperiments() []*Experiment {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var result []*Experiment
	for _, exp := range e.experiments {
		if exp.Status == "running" {
			copy := *exp
			result = append(result, &copy)
		}
	}
	return result
}

// PauseExperiment pauses an experiment.
func (e *ExperimentEngine) PauseExperiment(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	exp, ok := e.experiments[id]
	if !ok {
		return fmt.Errorf("experiment not found: %s", id)
	}

	exp.Status = "paused"
	return nil
}

// ResumeExperiment resumes a paused experiment.
func (e *ExperimentEngine) ResumeExperiment(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	exp, ok := e.experiments[id]
	if !ok {
		return fmt.Errorf("experiment not found: %s", id)
	}

	if exp.Status != "paused" {
		return fmt.Errorf("experiment is not paused: %s", exp.Status)
	}

	exp.Status = "running"
	return nil
}

// ConcludeExperiment concludes an experiment with a winner.
func (e *ExperimentEngine) ConcludeExperiment(id, winnerVariantID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	exp, ok := e.experiments[id]
	if !ok {
		return fmt.Errorf("experiment not found: %s", id)
	}

	now := time.Now()
	exp.Status = "concluded"
	exp.ConcludedAt = &now
	exp.WinnerVariantID = winnerVariantID

	return nil
}

// --- Variant Assignment ---

// AssignVariant determines which variant a page should receive.
// Uses deterministic hashing so the same page always gets the same variant.
func (e *ExperimentEngine) AssignVariant(experimentID, url string) *ExperimentVariant {
	e.mu.RLock()
	defer e.mu.RUnlock()

	exp, ok := e.experiments[experimentID]
	if !ok || exp.Status != "running" {
		return nil
	}

	// Check if page matches filter
	if !e.matchesFilter(url, exp.PageFilter) {
		return nil
	}

	// Deterministic hash-based assignment
	bucket := e.hashToBucket(experimentID, url)

	// Find which variant this bucket falls into
	cumulative := 0
	for i := range exp.Variants {
		cumulative += exp.Variants[i].TrafficPercent
		if bucket < cumulative {
			return &exp.Variants[i]
		}
	}

	// Fallback to last variant
	return &exp.Variants[len(exp.Variants)-1]
}

// GetVariantForPage returns the variant assignment for a page across all active experiments.
func (e *ExperimentEngine) GetVariantForPage(siteKey, url, schemaType string) *ExperimentVariant {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, exp := range e.experiments {
		if exp.Status != "running" {
			continue
		}
		if exp.SiteKey != siteKey {
			continue
		}
		if exp.SchemaType != "" && exp.SchemaType != schemaType {
			continue
		}
		if !e.matchesFilter(url, exp.PageFilter) {
			continue
		}

		// This page is in an active experiment
		return e.AssignVariant(exp.ID, url)
	}

	return nil // No active experiment for this page
}

func (e *ExperimentEngine) hashToBucket(experimentID, url string) int {
	// Deterministic hash: same experiment+url always returns same bucket
	h := sha256.Sum256([]byte(experimentID + ":" + url))
	// Use first 8 bytes as uint64, mod 100 for bucket
	num := binary.BigEndian.Uint64(h[:8])
	return int(num % 100)
}

func (e *ExperimentEngine) matchesFilter(url string, filter PageFilter) bool {
	// If no patterns specified, include all
	if len(filter.PathPatterns) == 0 && len(filter.PageTypes) == 0 {
		// Check sample percent
		if filter.SamplePercent > 0 && filter.SamplePercent < 100 {
			h := sha256.Sum256([]byte(url))
			bucket := int(binary.BigEndian.Uint64(h[:8]) % 100)
			return bucket < filter.SamplePercent
		}
		return true
	}

	// Check path patterns
	for _, pattern := range filter.PathPatterns {
		if matchPattern(pattern, url) {
			// Check exclusions
			for _, exclude := range filter.ExcludePatterns {
				if matchPattern(exclude, url) {
					return false
				}
			}
			return true
		}
	}

	return false
}

// --- Result Recording ---

// RecordImpression records that a page was served with a variant.
func (e *ExperimentEngine) RecordImpression(experimentID, variantID string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	results, ok := e.results[experimentID]
	if !ok {
		return
	}

	vr, ok := results.VariantResults[variantID]
	if !ok {
		return
	}

	vr.Impressions++
	vr.LastObservation = time.Now()
}

// RecordPush records a schema push for a variant.
func (e *ExperimentEngine) RecordPush(experimentID, variantID string, validationPassed bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	results, ok := e.results[experimentID]
	if !ok {
		return
	}

	vr, ok := results.VariantResults[variantID]
	if !ok {
		return
	}

	vr.Pushes++
	if validationPassed {
		vr.ValidationSuccess++
	} else {
		vr.ValidationFailure++
	}
	vr.LastObservation = time.Now()
}

// RecordCrawlerVisit records a crawler visit for a variant.
func (e *ExperimentEngine) RecordCrawlerVisit(experimentID, variantID string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	results, ok := e.results[experimentID]
	if !ok {
		return
	}

	vr, ok := results.VariantResults[variantID]
	if !ok {
		return
	}

	vr.CrawlerVisits++
	vr.LastObservation = time.Now()
}

// RecordRichResult records rich result eligibility for a variant.
func (e *ExperimentEngine) RecordRichResult(experimentID, variantID string, eligible bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	results, ok := e.results[experimentID]
	if !ok {
		return
	}

	vr, ok := results.VariantResults[variantID]
	if !ok {
		return
	}

	if eligible {
		vr.RichResultEligible++
	}
	vr.LastObservation = time.Now()
}

// RecordEffectiveness records an effectiveness score for a variant.
func (e *ExperimentEngine) RecordEffectiveness(experimentID, variantID string, score float64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	results, ok := e.results[experimentID]
	if !ok {
		return
	}

	vr, ok := results.VariantResults[variantID]
	if !ok {
		return
	}

	vr.TotalEffectiveness += score
	if vr.Impressions > 0 {
		vr.AvgEffectiveness = vr.TotalEffectiveness / float64(vr.Impressions)
	}
	vr.LastObservation = time.Now()
}

// --- Analysis ---

// AnalyzeExperiment analyzes results and determines if there's a winner.
func (e *ExperimentEngine) AnalyzeExperiment(experimentID string) *ExperimentAnalysis {
	e.mu.Lock()
	defer e.mu.Unlock()

	exp, ok := e.experiments[experimentID]
	if !ok {
		return nil
	}

	results, ok := e.results[experimentID]
	if !ok {
		return nil
	}

	analysis := &ExperimentAnalysis{
		ExperimentID:   experimentID,
		ExperimentName: exp.Name,
		Status:         exp.Status,
		DaysRunning:    int(time.Since(exp.StartedAt).Hours() / 24),
		VariantStats:   make([]VariantStats, 0, len(exp.Variants)),
	}

	// Calculate stats for each variant
	var controlStats *VariantStats
	var bestTreatment *VariantStats

	for _, variant := range exp.Variants {
		vr := results.VariantResults[variant.ID]
		if vr == nil {
			continue
		}

		stats := VariantStats{
			VariantID:      variant.ID,
			VariantName:    variant.Name,
			IsControl:      variant.IsControl,
			TrafficPercent: variant.TrafficPercent,
			SampleSize:     vr.Impressions,
		}

		// Calculate rates
		if vr.Impressions > 0 {
			stats.CrawlerRate = float64(vr.CrawlerVisits) / float64(vr.Impressions)
			stats.RichResultRate = float64(vr.RichResultEligible) / float64(vr.Impressions)
			stats.AvgEffectiveness = vr.AvgEffectiveness
		}

		if vr.Pushes > 0 {
			stats.ValidationRate = float64(vr.ValidationSuccess) / float64(vr.Pushes)
		}

		// Composite score
		stats.CompositeScore = (stats.CrawlerRate * 0.3) +
			(stats.RichResultRate * 0.3) +
			(stats.ValidationRate * 0.2) +
			(stats.AvgEffectiveness * 0.2)

		analysis.VariantStats = append(analysis.VariantStats, stats)

		if variant.IsControl {
			controlStats = &stats
		} else if bestTreatment == nil || stats.CompositeScore > bestTreatment.CompositeScore {
			bestTreatment = &stats
		}
	}

	// Determine winner
	analysis.HasSufficientData = e.hasSufficientData(results)

	if analysis.HasSufficientData && controlStats != nil && bestTreatment != nil {
		lift := 0.0
		if controlStats.CompositeScore > 0 {
			lift = (bestTreatment.CompositeScore - controlStats.CompositeScore) / controlStats.CompositeScore
		}
		analysis.Lift = lift

		// Statistical significance (simplified)
		analysis.Confidence = e.calculateConfidence(controlStats, bestTreatment)

		if analysis.Confidence >= e.config.ConfidenceThreshold {
			if lift >= e.config.MinEffectSize {
				analysis.Winner = bestTreatment.VariantID
				analysis.Recommendation = "promote_treatment"
			} else if lift <= -e.config.MinEffectSize {
				analysis.Winner = controlStats.VariantID
				analysis.Recommendation = "keep_control"
			} else {
				analysis.Recommendation = "no_significant_difference"
			}
		} else {
			analysis.Recommendation = "continue_experiment"
		}
	} else {
		analysis.Recommendation = "insufficient_data"
	}

	// Check for auto-conclusion
	if exp.Status == "running" && analysis.DaysRunning >= e.config.MaxDurationDays {
		analysis.Recommendation = "conclude_max_duration"
	}

	// Update results
	results.LastAnalyzed = time.Now()
	results.CurrentWinner = analysis.Winner
	results.Confidence = analysis.Confidence
	results.Recommendation = analysis.Recommendation

	return analysis
}

// ExperimentAnalysis contains analysis results.
type ExperimentAnalysis struct {
	ExperimentID      string         `json:"experiment_id"`
	ExperimentName    string         `json:"experiment_name"`
	Status            string         `json:"status"`
	DaysRunning       int            `json:"days_running"`
	VariantStats      []VariantStats `json:"variant_stats"`
	HasSufficientData bool           `json:"has_sufficient_data"`
	Winner            string         `json:"winner,omitempty"`
	Lift              float64        `json:"lift"`
	Confidence        float64        `json:"confidence"`
	Recommendation    string         `json:"recommendation"`
}

// VariantStats contains statistics for a variant.
type VariantStats struct {
	VariantID        string  `json:"variant_id"`
	VariantName      string  `json:"variant_name"`
	IsControl        bool    `json:"is_control"`
	TrafficPercent   int     `json:"traffic_percent"`
	SampleSize       int     `json:"sample_size"`
	CrawlerRate      float64 `json:"crawler_rate"`
	RichResultRate   float64 `json:"rich_result_rate"`
	ValidationRate   float64 `json:"validation_rate"`
	AvgEffectiveness float64 `json:"avg_effectiveness"`
	CompositeScore   float64 `json:"composite_score"`
}

func (e *ExperimentEngine) hasSufficientData(results *ExperimentResults) bool {
	for _, vr := range results.VariantResults {
		if vr.Impressions < e.config.MinSampleSize {
			return false
		}
	}
	return true
}

func (e *ExperimentEngine) calculateConfidence(control, treatment *VariantStats) float64 {
	// Simplified confidence calculation
	// Real implementation would use proper statistical tests (t-test, chi-squared, etc.)

	if control.SampleSize < 30 || treatment.SampleSize < 30 {
		return 0.0
	}

	// Use sample sizes to estimate confidence
	// More samples = higher confidence
	totalSamples := control.SampleSize + treatment.SampleSize
	sampleFactor := math.Min(1.0, float64(totalSamples)/1000.0)

	// Effect size factor
	diff := math.Abs(treatment.CompositeScore - control.CompositeScore)
	avg := (treatment.CompositeScore + control.CompositeScore) / 2
	effectSize := 0.0
	if avg > 0 {
		effectSize = diff / avg
	}
	effectFactor := math.Min(1.0, effectSize*10)

	// Combined confidence (simplified)
	confidence := sampleFactor * 0.6 + effectFactor * 0.4

	return math.Min(0.99, confidence)
}

// --- Auto-promotion ---

// PromoteWinner promotes the winning variant to 100% traffic.
func (e *ExperimentEngine) PromoteWinner(experimentID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	exp, ok := e.experiments[experimentID]
	if !ok {
		return fmt.Errorf("experiment not found: %s", experimentID)
	}

	if exp.WinnerVariantID == "" {
		return fmt.Errorf("no winner declared for experiment: %s", experimentID)
	}

	// This would integrate with the site registry to update the default schema config
	// For now, just mark as concluded
	now := time.Now()
	exp.Status = "concluded"
	exp.ConcludedAt = &now

	return nil
}

// CheckAndAutoPromote checks all experiments and auto-promotes winners if configured.
func (e *ExperimentEngine) CheckAndAutoPromote() []string {
	if !e.config.AutoPromoteWinners {
		return nil
	}

	var promoted []string

	for id := range e.experiments {
		analysis := e.AnalyzeExperiment(id)
		if analysis == nil {
			continue
		}

		if analysis.Recommendation == "promote_treatment" && analysis.Confidence >= e.config.ConfidenceThreshold {
			if err := e.ConcludeExperiment(id, analysis.Winner); err == nil {
				promoted = append(promoted, id)
			}
		}
	}

	return promoted
}

// --- Persistence ---

// Save persists the experiment engine state.
func (e *ExperimentEngine) Save() error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.filePath == "" {
		return nil
	}

	state := struct {
		Experiments map[string]*Experiment       `json:"experiments"`
		Results     map[string]*ExperimentResults `json:"results"`
	}{
		Experiments: e.experiments,
		Results:     e.results,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(e.filePath, data, 0644)
}

func (e *ExperimentEngine) load() error {
	if e.filePath == "" {
		return nil
	}

	data, err := os.ReadFile(e.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var state struct {
		Experiments map[string]*Experiment       `json:"experiments"`
		Results     map[string]*ExperimentResults `json:"results"`
	}

	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	if state.Experiments != nil {
		e.experiments = state.Experiments
	}
	if state.Results != nil {
		e.results = state.Results
	}

	return nil
}

// Stats returns experiment engine statistics.
func (e *ExperimentEngine) Stats() ExperimentStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	stats := ExperimentStats{
		TotalExperiments: len(e.experiments),
		ByStatus:         make(map[string]int),
	}

	for _, exp := range e.experiments {
		stats.ByStatus[exp.Status]++
		if exp.Status == "running" {
			stats.ActiveExperiments++
		}
	}

	return stats
}

// ExperimentStats contains experiment engine statistics.
type ExperimentStats struct {
	TotalExperiments  int            `json:"total_experiments"`
	ActiveExperiments int            `json:"active_experiments"`
	ByStatus          map[string]int `json:"by_status"`
}
