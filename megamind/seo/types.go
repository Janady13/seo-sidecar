package seo

import "time"

// PageObservation represents a detected page change.
type PageObservation struct {
	URL         string    `json:"url"`
	SiteKey     string    `json:"site_key"`
	Path        string    `json:"path"`
	ContentHash string    `json:"content_hash"`
	DetectedType string   `json:"detected_type"`
	LastSeen    time.Time `json:"last_seen"`
}

// SchemaCandidate represents a generated schema ready for validation.
type SchemaCandidate struct {
	URL         string    `json:"url"`
	SiteKey     string    `json:"site_key"`
	SchemaKey   string    `json:"schema_key"`
	JSONLD      string    `json:"jsonld"`
	Type        string    `json:"type"`
	GeneratedAt time.Time `json:"generated_at"`
}

// SchemaDecision represents the diff gate output.
type SchemaDecision struct {
	ShouldPush      bool    `json:"should_push"`
	Reason          string  `json:"reason"`
	DiffScore       float64 `json:"diff_score"`
	PreviousVersion string  `json:"previous_version"`
}

// SiteProfile defines schema behavior for a site.
type SiteProfile struct {
	SiteKey              string            `json:"site_key"`
	BaseURL              string            `json:"base_url"`
	SiteCategory         string            `json:"site_category"`
	AllowedSchemaTypes   []string          `json:"allowed_schema_types"`
	PagePatternRules     []PagePatternRule `json:"page_pattern_rules"`
	SidecarKeyStrategy   string            `json:"sidecar_key_strategy"`
	ChangeDetectionModes []string          `json:"change_detection_modes"`
	CrawlIncludePatterns []string          `json:"crawl_include_patterns"`
	CrawlExcludePatterns []string          `json:"crawl_exclude_patterns"`
	ValidationPolicy     ValidationPolicy  `json:"validation_policy"`
	PushPolicy           PushPolicy        `json:"push_policy"`
	Priority             int               `json:"priority"`
}

// PagePatternRule maps URL patterns to schema types.
type PagePatternRule struct {
	Pattern    string `json:"pattern"`
	SchemaType string `json:"schema_type"`
	SchemaKey  string `json:"schema_key"`
}

// ValidationPolicy defines validation behavior.
type ValidationPolicy struct {
	RequireContext    bool     `json:"require_context"`
	AllowedTypes      []string `json:"allowed_types"`
	MaxSizeBytes      int      `json:"max_size_bytes"`
	RequireMainEntity bool     `json:"require_main_entity"`
}

// PushPolicy defines push behavior.
type PushPolicy struct {
	MinDiffScore     float64 `json:"min_diff_score"`
	CooldownMinutes  int     `json:"cooldown_minutes"`
	RequireValidation bool   `json:"require_validation"`
	DryRun           bool    `json:"dry_run"`
}

// SchemaJob represents a queued schema operation.
type SchemaJob struct {
	JobID       string         `json:"job_id"`
	SiteKey     string         `json:"site_key"`
	URL         string         `json:"url"`
	JobType     string         `json:"job_type"`
	Priority    int            `json:"priority"`
	Attempts    int            `json:"attempts"`
	MaxAttempts int            `json:"max_attempts"`
	ScheduledAt time.Time      `json:"scheduled_at"`
	Payload     map[string]any `json:"payload"`
}

// LearningEntry records schema push outcomes.
type LearningEntry struct {
	URL                string    `json:"url"`
	SiteKey            string    `json:"site_key"`
	SchemaType         string    `json:"schema_type"`
	PageClass          string    `json:"page_class"`
	Pushed             bool      `json:"pushed"`
	DiffScore          float64   `json:"diff_score"`
	ValidationPassed   bool      `json:"validation_passed"`
	ResponseTimeMs     int64     `json:"response_time_ms"`
	Result             string    `json:"result"`
	EffectivenessScore float64   `json:"effectiveness_score"`
	Recommendation     string    `json:"recommendation"`
	ExperimentID       string    `json:"experiment_id,omitempty"`
	VariantID          string    `json:"variant_id,omitempty"`
	Timestamp          time.Time `json:"timestamp"`
}

// CacheEntry stores state for a page/schema pair.
type CacheEntry struct {
	URL              string    `json:"url"`
	SchemaKey        string    `json:"schema_key"`
	LastContentHash  string    `json:"last_content_hash"`
	LastSchemaHash   string    `json:"last_schema_hash"`
	LastPushedVersion int      `json:"last_pushed_version"`
	LastPushTime     time.Time `json:"last_push_time"`
	LastValidation   bool      `json:"last_validation"`
	LastDiffScore    float64   `json:"last_diff_score"`
}
