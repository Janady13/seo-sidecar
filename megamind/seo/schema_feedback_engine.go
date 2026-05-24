package seo

import (
	"encoding/json"
	"math"
	"os"
	"sort"
	"sync"
	"time"
)

// FeedbackEngine learns which schema strategies produce value.
type FeedbackEngine struct {
	mu           sync.RWMutex
	observations map[string]*SchemaObservation // key: siteKey:schemaType
	policies     map[string]*SchemaPolicy      // key: siteKey:schemaType
	signals      []FeedbackSignal
	filePath     string
	config       FeedbackConfig

	// GA integration
	gaSource       *GASignalSource
	gaBaselines    map[string]map[string]*GAPageMetrics
	lastGASync     time.Time
	gaSyncInterval time.Duration
}

// FeedbackConfig tunes the feedback engine.
type FeedbackConfig struct {
	MinObservationsForPolicy int     // Minimum observations before making recommendations
	DecayFactor              float64 // How fast old observations lose weight (0-1)
	HighValueThreshold       float64 // Score above this = high value schema
	LowValueThreshold        float64 // Score below this = consider retiring
	ChurnPenaltyWeight       float64 // How much to penalize frequent updates
	StabilityBonusWeight     float64 // How much to reward stable schemas
}

// SchemaObservation tracks outcomes for a schema type on a site.
type SchemaObservation struct {
	SiteKey       string    `json:"site_key"`
	SchemaType    string    `json:"schema_type"`
	FirstSeen     time.Time `json:"first_seen"`
	LastUpdated   time.Time `json:"last_updated"`
	TotalPushes   int       `json:"total_pushes"`
	SuccessCount  int       `json:"success_count"`
	FailureCount  int       `json:"failure_count"`
	ValidationOK  int       `json:"validation_ok"`
	ValidationFail int      `json:"validation_fail"`

	// Effectiveness signals
	CrawlerVisits      []CrawlerVisit  `json:"crawler_visits"`
	RichResultSignals  []RichResultSignal `json:"rich_result_signals"`

	// Churn tracking
	UpdateFrequency    float64   `json:"update_frequency"` // updates per day
	LastPushTimes      []time.Time `json:"last_push_times"`

	// Value metrics
	EffectivenessScore float64 `json:"effectiveness_score"`
	ConfidenceScore    float64 `json:"confidence_score"`
	ValueScore         float64 `json:"value_score"`
}

// CrawlerVisit records a crawler fetch event.
type CrawlerVisit struct {
	Timestamp  time.Time `json:"timestamp"`
	Crawler    string    `json:"crawler"` // googlebot, bingbot, gpt, claude, etc.
	URL        string    `json:"url"`
	StatusCode int       `json:"status_code"`
}

// RichResultSignal records rich result eligibility.
type RichResultSignal struct {
	Timestamp   time.Time `json:"timestamp"`
	SchemaType  string    `json:"schema_type"`
	Eligible    bool      `json:"eligible"`
	Warnings    []string  `json:"warnings,omitempty"`
	Source      string    `json:"source"` // validation_api, search_console, manual
}

// FeedbackSignal is a raw signal from any source.
type FeedbackSignal struct {
	Timestamp   time.Time      `json:"timestamp"`
	SiteKey     string         `json:"site_key"`
	SchemaType  string         `json:"schema_type"`
	SignalType  string         `json:"signal_type"`
	Value       float64        `json:"value"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// SchemaPolicy is a learned recommendation for a schema type.
type SchemaPolicy struct {
	SiteKey          string    `json:"site_key"`
	SchemaType       string    `json:"schema_type"`
	Recommendation   string    `json:"recommendation"` // expand, keep, simplify, retire, observe
	Confidence       float64   `json:"confidence"`
	OptimalFrequency string    `json:"optimal_frequency"` // daily, weekly, on-change, rare
	ValueScore       float64   `json:"value_score"`
	Reasoning        []string  `json:"reasoning"`
	LastEvaluated    time.Time `json:"last_evaluated"`
}

// NewFeedbackEngine creates a feedback engine.
func NewFeedbackEngine(filePath string, config FeedbackConfig) *FeedbackEngine {
	if config.MinObservationsForPolicy == 0 {
		config.MinObservationsForPolicy = 5
	}
	if config.DecayFactor == 0 {
		config.DecayFactor = 0.95
	}
	if config.HighValueThreshold == 0 {
		config.HighValueThreshold = 0.7
	}
	if config.LowValueThreshold == 0 {
		config.LowValueThreshold = 0.3
	}
	if config.ChurnPenaltyWeight == 0 {
		config.ChurnPenaltyWeight = 0.2
	}
	if config.StabilityBonusWeight == 0 {
		config.StabilityBonusWeight = 0.1
	}

	engine := &FeedbackEngine{
		observations: make(map[string]*SchemaObservation),
		policies:     make(map[string]*SchemaPolicy),
		signals:      make([]FeedbackSignal, 0),
		filePath:     filePath,
		config:       config,
	}

	engine.load()

	return engine
}

// --- Signal ingestion ---

// RecordPush records a schema push outcome.
func (e *FeedbackEngine) RecordPush(siteKey, schemaType string, success bool, validationPassed bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := e.key(siteKey, schemaType)
	obs := e.getOrCreateObservation(siteKey, schemaType)

	obs.TotalPushes++
	obs.LastUpdated = time.Now()
	obs.LastPushTimes = append(obs.LastPushTimes, time.Now())

	// Keep only last 100 push times
	if len(obs.LastPushTimes) > 100 {
		obs.LastPushTimes = obs.LastPushTimes[len(obs.LastPushTimes)-100:]
	}

	if success {
		obs.SuccessCount++
	} else {
		obs.FailureCount++
	}

	if validationPassed {
		obs.ValidationOK++
	} else {
		obs.ValidationFail++
	}

	// Update frequency calculation
	obs.UpdateFrequency = e.calculateUpdateFrequency(obs.LastPushTimes)

	e.observations[key] = obs
	e.recalculateScores(key)
}

// RecordCrawlerVisit records a crawler fetch event.
func (e *FeedbackEngine) RecordCrawlerVisit(siteKey, schemaType, crawler, url string, statusCode int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := e.key(siteKey, schemaType)
	obs := e.getOrCreateObservation(siteKey, schemaType)

	obs.CrawlerVisits = append(obs.CrawlerVisits, CrawlerVisit{
		Timestamp:  time.Now(),
		Crawler:    crawler,
		URL:        url,
		StatusCode: statusCode,
	})

	// Keep only last 1000 visits
	if len(obs.CrawlerVisits) > 1000 {
		obs.CrawlerVisits = obs.CrawlerVisits[len(obs.CrawlerVisits)-1000:]
	}

	e.observations[key] = obs
	e.recalculateScores(key)
}

// RecordRichResultSignal records rich result eligibility.
func (e *FeedbackEngine) RecordRichResultSignal(siteKey, schemaType string, eligible bool, warnings []string, source string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := e.key(siteKey, schemaType)
	obs := e.getOrCreateObservation(siteKey, schemaType)

	obs.RichResultSignals = append(obs.RichResultSignals, RichResultSignal{
		Timestamp:  time.Now(),
		SchemaType: schemaType,
		Eligible:   eligible,
		Warnings:   warnings,
		Source:     source,
	})

	// Keep only last 100 signals
	if len(obs.RichResultSignals) > 100 {
		obs.RichResultSignals = obs.RichResultSignals[len(obs.RichResultSignals)-100:]
	}

	e.observations[key] = obs
	e.recalculateScores(key)
}

// RecordSignal records a generic feedback signal.
func (e *FeedbackEngine) RecordSignal(signal FeedbackSignal) {
	e.mu.Lock()
	defer e.mu.Unlock()

	signal.Timestamp = time.Now()
	e.signals = append(e.signals, signal)

	// Keep only last 10000 signals
	if len(e.signals) > 10000 {
		e.signals = e.signals[len(e.signals)-10000:]
	}

	// Update observation if applicable
	if signal.SiteKey != "" && signal.SchemaType != "" {
		key := e.key(signal.SiteKey, signal.SchemaType)
		e.recalculateScores(key)
	}
}

// --- Score calculation ---

func (e *FeedbackEngine) recalculateScores(key string) {
	obs, ok := e.observations[key]
	if !ok {
		return
	}

	// Effectiveness score: success rate + validation rate + crawler engagement
	successRate := 0.0
	if obs.TotalPushes > 0 {
		successRate = float64(obs.SuccessCount) / float64(obs.TotalPushes)
	}

	validationRate := 0.0
	totalValidations := obs.ValidationOK + obs.ValidationFail
	if totalValidations > 0 {
		validationRate = float64(obs.ValidationOK) / float64(totalValidations)
	}

	crawlerEngagement := e.calculateCrawlerEngagement(obs)
	richResultRate := e.calculateRichResultRate(obs)

	// Weighted effectiveness
	obs.EffectivenessScore = (successRate * 0.3) +
		(validationRate * 0.2) +
		(crawlerEngagement * 0.3) +
		(richResultRate * 0.2)

	// Confidence score: based on observation count
	obs.ConfidenceScore = math.Min(1.0, float64(obs.TotalPushes)/float64(e.config.MinObservationsForPolicy*2))

	// Churn penalty
	churnPenalty := 0.0
	if obs.UpdateFrequency > 10 { // More than 10 updates per day is churny
		churnPenalty = math.Min(0.5, (obs.UpdateFrequency-10)*0.05)
	}

	// Stability bonus
	stabilityBonus := 0.0
	if obs.UpdateFrequency > 0 && obs.UpdateFrequency < 1 { // Less than daily
		stabilityBonus = e.config.StabilityBonusWeight * (1 - obs.UpdateFrequency)
	}

	// Final value score
	obs.ValueScore = obs.EffectivenessScore - (churnPenalty * e.config.ChurnPenaltyWeight) + stabilityBonus

	// Clamp to 0-1
	obs.ValueScore = math.Max(0, math.Min(1, obs.ValueScore))
}

func (e *FeedbackEngine) calculateUpdateFrequency(pushTimes []time.Time) float64 {
	if len(pushTimes) < 2 {
		return 0
	}

	// Look at last 7 days
	cutoff := time.Now().AddDate(0, 0, -7)
	recentCount := 0
	for _, t := range pushTimes {
		if t.After(cutoff) {
			recentCount++
		}
	}

	return float64(recentCount) / 7.0 // updates per day
}

func (e *FeedbackEngine) calculateCrawlerEngagement(obs *SchemaObservation) float64 {
	if len(obs.CrawlerVisits) == 0 {
		return 0.5 // neutral if no data
	}

	// Count recent visits (last 7 days)
	cutoff := time.Now().AddDate(0, 0, -7)
	recentVisits := 0
	for _, v := range obs.CrawlerVisits {
		if v.Timestamp.After(cutoff) {
			recentVisits++
		}
	}

	// Normalize: 10+ visits per week = full engagement
	return math.Min(1.0, float64(recentVisits)/10.0)
}

func (e *FeedbackEngine) calculateRichResultRate(obs *SchemaObservation) float64 {
	if len(obs.RichResultSignals) == 0 {
		return 0.5 // neutral if no data
	}

	// Look at recent signals
	cutoff := time.Now().AddDate(0, 0, -30)
	eligible := 0
	total := 0
	for _, s := range obs.RichResultSignals {
		if s.Timestamp.After(cutoff) {
			total++
			if s.Eligible {
				eligible++
			}
		}
	}

	if total == 0 {
		return 0.5
	}

	return float64(eligible) / float64(total)
}

// --- Policy generation ---

// EvaluatePolicy generates a recommendation for a schema type.
func (e *FeedbackEngine) EvaluatePolicy(siteKey, schemaType string) *SchemaPolicy {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := e.key(siteKey, schemaType)
	obs, ok := e.observations[key]
	if !ok {
		return &SchemaPolicy{
			SiteKey:        siteKey,
			SchemaType:     schemaType,
			Recommendation: "observe",
			Confidence:     0,
			Reasoning:      []string{"No observations yet"},
			LastEvaluated:  time.Now(),
		}
	}

	policy := &SchemaPolicy{
		SiteKey:       siteKey,
		SchemaType:    schemaType,
		Confidence:    obs.ConfidenceScore,
		ValueScore:    obs.ValueScore,
		LastEvaluated: time.Now(),
		Reasoning:     make([]string, 0),
	}

	// Determine recommendation based on value score and confidence
	if obs.ConfidenceScore < 0.5 {
		policy.Recommendation = "observe"
		policy.Reasoning = append(policy.Reasoning, "Insufficient observations for confident recommendation")
	} else if obs.ValueScore >= e.config.HighValueThreshold {
		policy.Recommendation = "expand"
		policy.Reasoning = append(policy.Reasoning, "High value schema - consider expanding coverage")
	} else if obs.ValueScore <= e.config.LowValueThreshold {
		policy.Recommendation = "simplify"
		policy.Reasoning = append(policy.Reasoning, "Low value schema - consider simplifying or retiring")
		if obs.ValueScore < 0.1 {
			policy.Recommendation = "retire"
			policy.Reasoning = append(policy.Reasoning, "Very low value - recommend retiring this schema type")
		}
	} else {
		policy.Recommendation = "keep"
		policy.Reasoning = append(policy.Reasoning, "Moderate value - maintain current approach")
	}

	// Determine optimal frequency
	if obs.UpdateFrequency > 5 {
		policy.OptimalFrequency = "rare"
		policy.Reasoning = append(policy.Reasoning, "High churn detected - reduce update frequency")
	} else if obs.UpdateFrequency > 1 {
		policy.OptimalFrequency = "weekly"
	} else if obs.UpdateFrequency > 0.1 {
		policy.OptimalFrequency = "on-change"
	} else {
		policy.OptimalFrequency = "on-change"
	}

	// Add specific insights
	if e.calculateCrawlerEngagement(obs) > 0.7 {
		policy.Reasoning = append(policy.Reasoning, "Strong crawler engagement observed")
	}
	if e.calculateRichResultRate(obs) > 0.8 {
		policy.Reasoning = append(policy.Reasoning, "High rich result eligibility rate")
	}
	if obs.ValidationFail > obs.ValidationOK {
		policy.Reasoning = append(policy.Reasoning, "Warning: High validation failure rate")
	}

	e.policies[key] = policy
	return policy
}

// EvaluateAllPolicies generates recommendations for all observed schemas.
func (e *FeedbackEngine) EvaluateAllPolicies() []*SchemaPolicy {
	e.mu.RLock()
	keys := make([]string, 0, len(e.observations))
	for k := range e.observations {
		keys = append(keys, k)
	}
	e.mu.RUnlock()

	policies := make([]*SchemaPolicy, 0, len(keys))
	for _, key := range keys {
		obs := e.observations[key]
		if obs != nil {
			policy := e.EvaluatePolicy(obs.SiteKey, obs.SchemaType)
			policies = append(policies, policy)
		}
	}

	return policies
}

// --- Query methods ---

// GetObservation returns observations for a schema type.
func (e *FeedbackEngine) GetObservation(siteKey, schemaType string) *SchemaObservation {
	e.mu.RLock()
	defer e.mu.RUnlock()

	key := e.key(siteKey, schemaType)
	if obs, ok := e.observations[key]; ok {
		copy := *obs
		return &copy
	}
	return nil
}

// GetPolicy returns the current policy for a schema type.
func (e *FeedbackEngine) GetPolicy(siteKey, schemaType string) *SchemaPolicy {
	e.mu.RLock()
	defer e.mu.RUnlock()

	key := e.key(siteKey, schemaType)
	if policy, ok := e.policies[key]; ok {
		copy := *policy
		return &copy
	}
	return nil
}

// ShouldPush returns whether a schema should be pushed based on policy.
func (e *FeedbackEngine) ShouldPush(siteKey, schemaType string) (bool, string) {
	policy := e.GetPolicy(siteKey, schemaType)
	if policy == nil {
		return true, "no policy - allow push"
	}

	switch policy.Recommendation {
	case "retire":
		return false, "schema type marked for retirement"
	case "simplify":
		// Allow but with warning
		return true, "schema type marked for simplification - consider reducing complexity"
	default:
		return true, "policy allows push"
	}
}

// GetTopPerformers returns the highest value schemas.
func (e *FeedbackEngine) GetTopPerformers(limit int) []*SchemaObservation {
	e.mu.RLock()
	defer e.mu.RUnlock()

	obs := make([]*SchemaObservation, 0, len(e.observations))
	for _, o := range e.observations {
		if o.ConfidenceScore >= 0.5 {
			obs = append(obs, o)
		}
	}

	sort.Slice(obs, func(i, j int) bool {
		return obs[i].ValueScore > obs[j].ValueScore
	})

	if limit > 0 && limit < len(obs) {
		obs = obs[:limit]
	}

	return obs
}

// GetUnderperformers returns schemas that should be reviewed.
func (e *FeedbackEngine) GetUnderperformers(limit int) []*SchemaObservation {
	e.mu.RLock()
	defer e.mu.RUnlock()

	obs := make([]*SchemaObservation, 0)
	for _, o := range e.observations {
		if o.ConfidenceScore >= 0.5 && o.ValueScore < e.config.LowValueThreshold {
			obs = append(obs, o)
		}
	}

	sort.Slice(obs, func(i, j int) bool {
		return obs[i].ValueScore < obs[j].ValueScore
	})

	if limit > 0 && limit < len(obs) {
		obs = obs[:limit]
	}

	return obs
}

// GetHighChurn returns schemas with excessive update frequency.
func (e *FeedbackEngine) GetHighChurn(threshold float64) []*SchemaObservation {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if threshold == 0 {
		threshold = 5.0 // 5 updates per day
	}

	obs := make([]*SchemaObservation, 0)
	for _, o := range e.observations {
		if o.UpdateFrequency > threshold {
			obs = append(obs, o)
		}
	}

	sort.Slice(obs, func(i, j int) bool {
		return obs[i].UpdateFrequency > obs[j].UpdateFrequency
	})

	return obs
}

// --- Insights ---

// GenerateInsights produces actionable insights from all observations.
func (e *FeedbackEngine) GenerateInsights() []Insight {
	e.mu.RLock()
	defer e.mu.RUnlock()

	insights := make([]Insight, 0)

	// Aggregate stats
	totalSchemas := len(e.observations)
	var totalPushes, totalSuccess, totalFailures int
	var avgValue float64
	schemaTypeStats := make(map[string][]float64)

	for _, obs := range e.observations {
		totalPushes += obs.TotalPushes
		totalSuccess += obs.SuccessCount
		totalFailures += obs.FailureCount
		avgValue += obs.ValueScore
		schemaTypeStats[obs.SchemaType] = append(schemaTypeStats[obs.SchemaType], obs.ValueScore)
	}

	if totalSchemas > 0 {
		avgValue /= float64(totalSchemas)
	}

	// Overall health insight
	if totalSchemas > 0 {
		successRate := float64(totalSuccess) / float64(totalPushes)
		if successRate < 0.9 {
			insights = append(insights, Insight{
				Type:     "warning",
				Category: "reliability",
				Message:  "Schema push success rate below 90%",
				Value:    successRate,
				Action:   "Review failing schemas and fix validation issues",
			})
		}
	}

	// Schema type effectiveness
	for schemaType, values := range schemaTypeStats {
		if len(values) >= 3 {
			avg := 0.0
			for _, v := range values {
				avg += v
			}
			avg /= float64(len(values))

			if avg >= e.config.HighValueThreshold {
				insights = append(insights, Insight{
					Type:     "success",
					Category: "effectiveness",
					Message:  schemaType + " schemas are highly effective",
					Value:    avg,
					Action:   "Consider expanding " + schemaType + " coverage",
				})
			} else if avg < e.config.LowValueThreshold {
				insights = append(insights, Insight{
					Type:     "warning",
					Category: "effectiveness",
					Message:  schemaType + " schemas showing low value",
					Value:    avg,
					Action:   "Review whether " + schemaType + " is worth maintaining",
				})
			}
		}
	}

	// Churn insights
	highChurn := e.GetHighChurn(5)
	if len(highChurn) > 0 {
		insights = append(insights, Insight{
			Type:     "warning",
			Category: "churn",
			Message:  "High update frequency detected on some schemas",
			Value:    float64(len(highChurn)),
			Action:   "Review throttling policies for churning schemas",
		})
	}

	// Top performers
	topPerformers := e.GetTopPerformers(3)
	if len(topPerformers) > 0 {
		for _, top := range topPerformers {
			insights = append(insights, Insight{
				Type:     "success",
				Category: "top_performer",
				Message:  top.SiteKey + ":" + top.SchemaType + " is a top performer",
				Value:    top.ValueScore,
				Action:   "Study this schema's approach for replication",
			})
		}
	}

	return insights
}

// Insight represents an actionable finding.
type Insight struct {
	Type     string  `json:"type"`     // success, warning, info
	Category string  `json:"category"` // effectiveness, reliability, churn, etc.
	Message  string  `json:"message"`
	Value    float64 `json:"value,omitempty"`
	Action   string  `json:"action"`
}

// --- Persistence ---

func (e *FeedbackEngine) key(siteKey, schemaType string) string {
	return siteKey + ":" + schemaType
}

func (e *FeedbackEngine) getOrCreateObservation(siteKey, schemaType string) *SchemaObservation {
	key := e.key(siteKey, schemaType)
	if obs, ok := e.observations[key]; ok {
		return obs
	}

	obs := &SchemaObservation{
		SiteKey:    siteKey,
		SchemaType: schemaType,
		FirstSeen:  time.Now(),
	}
	e.observations[key] = obs
	return obs
}

// Save persists the feedback engine state.
func (e *FeedbackEngine) Save() error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.filePath == "" {
		return nil
	}

	state := struct {
		Observations map[string]*SchemaObservation `json:"observations"`
		Policies     map[string]*SchemaPolicy      `json:"policies"`
		Signals      []FeedbackSignal              `json:"signals"`
	}{
		Observations: e.observations,
		Policies:     e.policies,
		Signals:      e.signals,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(e.filePath, data, 0644)
}

func (e *FeedbackEngine) load() error {
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
		Observations map[string]*SchemaObservation `json:"observations"`
		Policies     map[string]*SchemaPolicy      `json:"policies"`
		Signals      []FeedbackSignal              `json:"signals"`
	}

	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	if state.Observations != nil {
		e.observations = state.Observations
	}
	if state.Policies != nil {
		e.policies = state.Policies
	}
	if state.Signals != nil {
		e.signals = state.Signals
	}

	return nil
}

// Stats returns feedback engine statistics.
func (e *FeedbackEngine) Stats() FeedbackStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	stats := FeedbackStats{
		TotalObservations: len(e.observations),
		TotalPolicies:     len(e.policies),
		TotalSignals:      len(e.signals),
		ByRecommendation:  make(map[string]int),
	}

	for _, policy := range e.policies {
		stats.ByRecommendation[policy.Recommendation]++
	}

	return stats
}

// FeedbackStats contains feedback engine statistics.
type FeedbackStats struct {
	TotalObservations int            `json:"total_observations"`
	TotalPolicies     int            `json:"total_policies"`
	TotalSignals      int            `json:"total_signals"`
	ByRecommendation  map[string]int `json:"by_recommendation"`
	GASignals         int            `json:"ga_signals,omitempty"`
	LastGASync        string         `json:"last_ga_sync,omitempty"`
}

// --- GA Signal Integration ---

// SetGASource connects a Google Analytics signal source.
func (e *FeedbackEngine) SetGASource(source *GASignalSource) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.gaSource = source
	e.gaSyncInterval = 15 * time.Minute
	e.gaBaselines = make(map[string]map[string]*GAPageMetrics)
}

// SyncGASignals fetches latest GA data and generates feedback signals.
func (e *FeedbackEngine) SyncGASignals() error {
	e.mu.Lock()
	source := e.gaSource
	e.mu.Unlock()

	if source == nil {
		return nil
	}

	// Fetch latest report
	report, err := source.FetchReport()
	if err != nil {
		return err
	}

	// Generate signals by comparing to baselines
	e.mu.Lock()
	signals := source.GenerateSignals(e.gaBaselines)

	// Process each signal
	for _, gaSignal := range signals {
		// Convert GA signal to feedback signal
		feedbackSignal := FeedbackSignal{
			Timestamp:  gaSignal.Timestamp,
			SiteKey:    gaSignal.SiteKey,
			SchemaType: "", // GA signals are page-level, not schema-level
			SignalType: "ga_" + gaSignal.SignalType,
			Value:      gaSignal.PercentChange,
			Metadata: map[string]any{
				"page_path":      gaSignal.PagePath,
				"current_value":  gaSignal.Value,
				"baseline_value": gaSignal.BaselineValue,
				"data_source":    gaSignal.DataSource,
			},
		}
		e.signals = append(e.signals, feedbackSignal)

		// Update observations for the site
		e.updateFromGASignal(gaSignal)
	}

	// Update baselines for next comparison
	e.gaBaselines = source.SnapshotBaselines()
	e.lastGASync = time.Now()

	// Keep only last 10000 signals
	if len(e.signals) > 10000 {
		e.signals = e.signals[len(e.signals)-10000:]
	}
	e.mu.Unlock()

	// Log report summary
	if report != nil && report.OverallStatus != "" {
		e.recordGAReportSummary(report)
	}

	return nil
}

// updateFromGASignal updates schema observations based on GA traffic signals.
func (e *FeedbackEngine) updateFromGASignal(signal GASignal) {
	// GA signals indicate traffic patterns which correlate with schema effectiveness.
	// A traffic increase after a schema push suggests the schema is working.
	// A traffic drop might indicate schema issues (though correlation != causation).

	// Look up what schema types exist for this site
	for key, obs := range e.observations {
		if obs.SiteKey == signal.SiteKey {
			// Apply GA signal as a modifier to effectiveness calculations

			// Traffic increase = positive signal
			if signal.SignalType == "traffic_increase" {
				obs.EffectivenessScore = math.Min(1.0, obs.EffectivenessScore*1.02)
			}

			// Traffic drop = negative signal (but minor weight)
			if signal.SignalType == "traffic_drop" {
				obs.EffectivenessScore = math.Max(0, obs.EffectivenessScore*0.99)
			}

			// Engagement increase = strong positive signal
			if signal.SignalType == "engagement_increase" {
				obs.EffectivenessScore = math.Min(1.0, obs.EffectivenessScore*1.05)
			}

			e.observations[key] = obs
			e.recalculateScores(key)
		}
	}
}

// recordGAReportSummary logs aggregate GA metrics as signals.
func (e *FeedbackEngine) recordGAReportSummary(report *GAReportPack) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for domain, domainData := range report.Domains {
		siteKey := domainToSiteKey(domain)

		// Create summary signal
		pageViews := domainData.Summary.PageViews
		if domainData.Summary.ScreenPageViews > 0 {
			pageViews = domainData.Summary.ScreenPageViews
		}

		e.signals = append(e.signals, FeedbackSignal{
			Timestamp:  time.Now(),
			SiteKey:    siteKey,
			SignalType: "ga_summary",
			Value:      float64(pageViews),
			Metadata: map[string]any{
				"source":           domainData.Source,
				"sessions":         domainData.Summary.Sessions,
				"total_users":      domainData.Summary.TotalUsers,
				"unique_visitors":  domainData.Summary.UniqueVisitors,
				"engaged_sessions": domainData.Summary.EngagedSessions,
				"date_range":       report.DateRange,
			},
		})
	}
}

// GetGAMetricsForPage returns GA metrics for a specific page.
func (e *FeedbackEngine) GetGAMetricsForPage(domain, path string) *GAPageMetrics {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.gaSource == nil {
		return nil
	}

	return e.gaSource.GetPageMetrics(domain, path)
}

// GetGASummary returns GA summary for a domain.
func (e *FeedbackEngine) GetGASummary(domain string) *GASummary {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.gaSource == nil {
		return nil
	}

	return e.gaSource.GetDomainSummary(domain)
}

// GetGATopPages returns top pages for a domain.
func (e *FeedbackEngine) GetGATopPages(domain string, limit int) []GAPage {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.gaSource == nil {
		return nil
	}

	return e.gaSource.GetTopPages(domain, limit)
}

// StartGASync starts background GA synchronization.
func (e *FeedbackEngine) StartGASync(done <-chan struct{}) {
	go func() {
		// Initial sync
		e.SyncGASignals()

		ticker := time.NewTicker(e.gaSyncInterval)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				e.SyncGASignals()
			}
		}
	}()
}
