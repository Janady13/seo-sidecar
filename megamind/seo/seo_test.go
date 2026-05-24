package seo

import (
	"testing"
	"time"
)

func TestCanonicalizer(t *testing.T) {
	c := NewCanonicalizer()

	// Test basic canonicalization
	json1 := []byte(`{"@type": "Article", "@context": "https://schema.org", "name": "Test"}`)
	json2 := []byte(`{"@context": "https://schema.org", "@type": "Article", "name": "Test"}`)

	hash1, err := c.Hash(json1)
	if err != nil {
		t.Fatalf("Hash error: %v", err)
	}

	hash2, err := c.Hash(json2)
	if err != nil {
		t.Fatalf("Hash error: %v", err)
	}

	if hash1 != hash2 {
		t.Errorf("Hashes should match for equivalent JSON: %s != %s", hash1, hash2)
	}

	// Test ignored fields
	json3 := []byte(`{"@type": "Article", "dateModified": "2024-01-01"}`)
	json4 := []byte(`{"@type": "Article", "dateModified": "2024-12-31"}`)

	hash3, _ := c.Hash(json3)
	hash4, _ := c.Hash(json4)

	if hash3 != hash4 {
		t.Errorf("Hashes should match when only ignored fields differ")
	}
}

func TestThrottle(t *testing.T) {
	throttle := NewThrottle(ThrottleConfig{
		MaxPushesPerHour:   5,
		MinIntervalBetween: time.Millisecond * 100,
		BurstAllowance:     2,
	})

	// First push should be allowed
	allowed, _ := throttle.AllowAndRecord("site1")
	if !allowed {
		t.Error("First push should be allowed")
	}

	// Immediate second push should be blocked (min interval)
	allowed, reason := throttle.Allow("site1")
	if allowed {
		t.Error("Immediate second push should be blocked")
	}
	if reason == "" {
		t.Error("Should have a reason for blocking")
	}

	// Wait for interval and try again
	time.Sleep(time.Millisecond * 150)
	allowed, _ = throttle.AllowAndRecord("site1")
	if !allowed {
		t.Error("Push after interval should be allowed")
	}
}

func TestJobQueueDeduplication(t *testing.T) {
	queue := NewJobQueue("")

	// Enqueue first job
	id1 := queue.EnqueueClassify("site1", "https://example.com/page1", 5)

	// Enqueue duplicate - should return same ID
	id2 := queue.EnqueueClassify("site1", "https://example.com/page1", 5)

	if id1 != id2 {
		t.Errorf("Duplicate jobs should return same ID: %s != %s", id1, id2)
	}

	// Queue should only have one job
	if queue.Len() != 1 {
		t.Errorf("Queue should have 1 job, got %d", queue.Len())
	}

	// Enqueue with higher priority - should update existing
	id3 := queue.EnqueueClassify("site1", "https://example.com/page1", 10)

	if id1 != id3 {
		t.Errorf("Priority update should return same ID: %s != %s", id1, id3)
	}

	if queue.Len() != 1 {
		t.Errorf("Queue should still have 1 job, got %d", queue.Len())
	}
}

func TestSiteRegistry(t *testing.T) {
	registry := NewSiteRegistry("")

	// Register a site
	registry.Register(&SiteProfile{
		SiteKey:            "testsite",
		BaseURL:            "https://testsite.com",
		SiteCategory:       "test",
		AllowedSchemaTypes: []string{"Article", "FAQPage"},
	})

	// Get site
	site := registry.Get("testsite")
	if site == nil {
		t.Fatal("Site should exist")
	}
	if site.BaseURL != "https://testsite.com" {
		t.Errorf("Wrong base URL: %s", site.BaseURL)
	}

	// Check schema type support
	if !registry.SupportsSchemaType("testsite", "Article") {
		t.Error("Should support Article")
	}
	if registry.SupportsSchemaType("testsite", "Product") {
		t.Error("Should not support Product")
	}
}

func TestExperimentAssignment(t *testing.T) {
	engine := NewExperimentEngine("", ExperimentConfig{})

	// Create 80/20 experiment
	exp, err := engine.Create80_20Experiment(
		"Test Experiment",
		"testsite",
		"Article",
		"Testing hypothesis",
		SchemaConfig{SchemaType: "Article"},
		PageFilter{},
	)
	if err != nil {
		t.Fatalf("Create experiment error: %v", err)
	}

	// Same URL should always get same variant
	var1 := engine.AssignVariant(exp.ID, "https://testsite.com/page1")
	var2 := engine.AssignVariant(exp.ID, "https://testsite.com/page1")

	if var1.ID != var2.ID {
		t.Error("Same URL should always get same variant")
	}

	// Different URLs may get different variants (probabilistic)
	controlCount := 0
	treatmentCount := 0
	for i := 0; i < 100; i++ {
		v := engine.AssignVariant(exp.ID, "https://testsite.com/page"+string(rune('0'+i)))
		if v.IsControl {
			controlCount++
		} else {
			treatmentCount++
		}
	}

	// Should be roughly 80/20 (allow some variance)
	if controlCount < 60 || controlCount > 95 {
		t.Errorf("Control should be ~80%%, got %d%%", controlCount)
	}
}

func TestFeedbackEngine(t *testing.T) {
	engine := NewFeedbackEngine("", FeedbackConfig{
		MinObservationsForPolicy: 5,
	})

	// Record some pushes
	for i := 0; i < 10; i++ {
		engine.RecordPush("site1", "Article", true, true)
	}

	// Get observation
	obs := engine.GetObservation("site1", "Article")
	if obs == nil {
		t.Fatal("Observation should exist")
	}
	if obs.TotalPushes != 10 {
		t.Errorf("Should have 10 pushes, got %d", obs.TotalPushes)
	}

	// Evaluate policy
	policy := engine.EvaluatePolicy("site1", "Article")
	if policy == nil {
		t.Fatal("Policy should exist")
	}
	if policy.Confidence < 0.5 {
		t.Error("Should have reasonable confidence with 10 observations")
	}
}

func TestPageClassifier(t *testing.T) {
	registry := NewSiteRegistry("")
	classifier := NewRuleBasedClassifier(registry)

	tests := []struct {
		url      string
		content  string
		expected string
	}{
		{"/about", "", "AboutPage"},
		{"/faq", "", "FAQPage"},
		{"/services", "", "Service"},
		{"/contact", "", "ContactPage"},
		{"/blog/post1", "<article>content</article>", "Article"},
	}

	for _, tt := range tests {
		result := classifier.Classify([]byte(tt.content), tt.url)
		if result != tt.expected {
			t.Errorf("Classify(%s) = %s, want %s", tt.url, result, tt.expected)
		}
	}
}

func TestValidator(t *testing.T) {
	registry := NewSiteRegistry("")
	validator := NewSchemaValidator(registry)

	// Valid Article schema
	validArticle := &SchemaCandidate{
		Type:   "Article",
		JSONLD: `{"@context": "https://schema.org", "@type": "Article", "headline": "Test"}`,
	}
	result := validator.Validate(validArticle)
	if !result.Valid {
		t.Errorf("Valid article should pass: %v", result.Errors)
	}

	// Invalid JSON
	invalidJSON := &SchemaCandidate{
		Type:   "Article",
		JSONLD: `{invalid json}`,
	}
	result = validator.Validate(invalidJSON)
	if result.Valid {
		t.Error("Invalid JSON should fail")
	}

	// Missing context
	missingContext := &SchemaCandidate{
		Type:   "Article",
		JSONLD: `{"@type": "Article", "headline": "Test"}`,
	}
	result = validator.Validate(missingContext)
	if result.Valid {
		t.Error("Missing context should fail")
	}
}

func TestGASignalGeneration(t *testing.T) {
	// Test GA signal source with baseline comparison
	source := NewGASignalSource("http://localhost:9090", "test-token")

	// Simulate indexed metrics
	source.mu.Lock()
	source.pageMetrics["thatdeveloperguy.com"] = map[string]*GAPageMetrics{
		"/": {
			Domain:    "thatdeveloperguy.com",
			Path:      "/",
			PageViews: 100,
		},
		"/about": {
			Domain:    "thatdeveloperguy.com",
			Path:      "/about",
			PageViews: 50,
		},
	}
	source.mu.Unlock()

	// Test snapshot baselines
	baselines := source.SnapshotBaselines()
	if baselines == nil {
		t.Fatal("Baselines should not be nil")
	}
	if baselines["thatdeveloperguy.com"] == nil {
		t.Fatal("Domain baselines should exist")
	}
	if baselines["thatdeveloperguy.com"]["/"].PageViews != 100 {
		t.Errorf("Baseline pageviews wrong: got %d, want 100", baselines["thatdeveloperguy.com"]["/"].PageViews)
	}

	// Test page metrics lookup
	metrics := source.GetPageMetrics("thatdeveloperguy.com", "/")
	if metrics == nil {
		t.Fatal("Metrics should exist for homepage")
	}
	if metrics.PageViews != 100 {
		t.Errorf("Page views wrong: got %d, want 100", metrics.PageViews)
	}
}

func TestFeedbackEngineGAIntegration(t *testing.T) {
	// Create feedback engine
	engine := NewFeedbackEngine("", FeedbackConfig{
		MinObservationsForPolicy: 5,
	})

	// Create GA source
	source := NewGASignalSource("http://localhost:9090", "test-token")

	// Connect GA source to feedback engine
	engine.SetGASource(source)

	// Record some pushes to create observations
	for i := 0; i < 10; i++ {
		engine.RecordPush("thatdeveloperguy", "Article", true, true)
	}

	// Check stats include GA fields
	stats := engine.Stats()
	if stats.TotalObservations == 0 {
		t.Error("Should have observations")
	}

	// Test that observation exists
	obs := engine.GetObservation("thatdeveloperguy", "Article")
	if obs == nil {
		t.Fatal("Observation should exist")
	}
	if obs.TotalPushes != 10 {
		t.Errorf("Should have 10 pushes, got %d", obs.TotalPushes)
	}
}

func TestDomainToSiteKey(t *testing.T) {
	tests := []struct {
		domain   string
		expected string
	}{
		{"thatdeveloperguy.com", "thatdeveloperguy"},
		{"thatcoputerdude.com", "thatcomputerdude"},
		{"thataiguy.org", "thataiguy"},
		{"unknown.com", "unknown.com"},
	}

	for _, tt := range tests {
		result := domainToSiteKey(tt.domain)
		if result != tt.expected {
			t.Errorf("domainToSiteKey(%s) = %s, want %s", tt.domain, result, tt.expected)
		}
	}
}
