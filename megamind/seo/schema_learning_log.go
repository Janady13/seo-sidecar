package seo

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// LearningLog records schema push outcomes for analysis.
type LearningLog struct {
	mu       sync.Mutex
	entries  []LearningEntry
	filePath string
	maxSize  int
}

// NewLearningLog creates a learning log.
func NewLearningLog(filePath string, maxSize int) *LearningLog {
	log := &LearningLog{
		entries:  make([]LearningEntry, 0),
		filePath: filePath,
		maxSize:  maxSize,
	}

	log.load()

	return log
}

// Record logs a schema operation outcome.
func (l *LearningLog) Record(entry LearningEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry.Timestamp = time.Now()
	l.entries = append(l.entries, entry)

	// Trim if over max size
	if l.maxSize > 0 && len(l.entries) > l.maxSize {
		l.entries = l.entries[len(l.entries)-l.maxSize:]
	}
}

// RecordPush logs a push operation (simplified).
func (l *LearningLog) RecordPush(
	url string,
	schemaType string,
	pushed bool,
	diffScore float64,
	validationPassed bool,
	responseTimeMs int64,
	result string,
) {
	l.Record(LearningEntry{
		URL:              url,
		SchemaType:       schemaType,
		Pushed:           pushed,
		DiffScore:        diffScore,
		ValidationPassed: validationPassed,
		ResponseTimeMs:   responseTimeMs,
		Result:           result,
	})
}

// RecordPushFull logs a push operation with all fields.
func (l *LearningLog) RecordPushFull(entry LearningEntry) {
	l.Record(entry)
}

// RecordFromCandidate logs from a schema candidate and decision.
func (l *LearningLog) RecordFromCandidate(
	candidate *SchemaCandidate,
	decision *SchemaDecision,
	validationPassed bool,
	responseTimeMs int64,
	result string,
) {
	l.Record(LearningEntry{
		URL:              candidate.URL,
		SiteKey:          candidate.SiteKey,
		SchemaType:       candidate.Type,
		Pushed:           decision.ShouldPush,
		DiffScore:        decision.DiffScore,
		ValidationPassed: validationPassed,
		ResponseTimeMs:   responseTimeMs,
		Result:           result,
	})
}

// RecordFromCandidateWithExperiment logs with experiment context.
func (l *LearningLog) RecordFromCandidateWithExperiment(
	candidate *SchemaCandidate,
	decision *SchemaDecision,
	validationPassed bool,
	responseTimeMs int64,
	result string,
	pageClass string,
	effectivenessScore float64,
	recommendation string,
	experimentID string,
	variantID string,
) {
	l.Record(LearningEntry{
		URL:                candidate.URL,
		SiteKey:            candidate.SiteKey,
		SchemaType:         candidate.Type,
		PageClass:          pageClass,
		Pushed:             decision.ShouldPush,
		DiffScore:          decision.DiffScore,
		ValidationPassed:   validationPassed,
		ResponseTimeMs:     responseTimeMs,
		Result:             result,
		EffectivenessScore: effectivenessScore,
		Recommendation:     recommendation,
		ExperimentID:       experimentID,
		VariantID:          variantID,
	})
}

// GetRecent returns recent entries.
func (l *LearningLog) GetRecent(limit int) []LearningEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	if limit <= 0 || limit > len(l.entries) {
		limit = len(l.entries)
	}

	start := len(l.entries) - limit
	result := make([]LearningEntry, limit)
	copy(result, l.entries[start:])

	return result
}

// GetBySchemaType returns entries for a specific schema type.
func (l *LearningLog) GetBySchemaType(schemaType string) []LearningEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	var result []LearningEntry
	for _, entry := range l.entries {
		if entry.SchemaType == schemaType {
			result = append(result, entry)
		}
	}

	return result
}

// GetByURL returns entries for a specific URL.
func (l *LearningLog) GetByURL(url string) []LearningEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	var result []LearningEntry
	for _, entry := range l.entries {
		if entry.URL == url {
			result = append(result, entry)
		}
	}

	return result
}

// Stats returns aggregate statistics.
func (l *LearningLog) Stats() LearningStats {
	l.mu.Lock()
	defer l.mu.Unlock()

	stats := LearningStats{
		TotalOperations:  len(l.entries),
		BySchemaType:     make(map[string]int),
		ByResult:         make(map[string]int),
	}

	var totalDiff float64
	var totalResponseTime int64

	for _, entry := range l.entries {
		if entry.Pushed {
			stats.TotalPushes++
		}
		if entry.ValidationPassed {
			stats.TotalValid++
		}

		stats.BySchemaType[entry.SchemaType]++
		stats.ByResult[entry.Result]++

		totalDiff += entry.DiffScore
		totalResponseTime += entry.ResponseTimeMs
	}

	if len(l.entries) > 0 {
		stats.AvgDiffScore = totalDiff / float64(len(l.entries))
		stats.AvgResponseTimeMs = totalResponseTime / int64(len(l.entries))
	}

	return stats
}

// LearningStats contains aggregate statistics.
type LearningStats struct {
	TotalOperations   int
	TotalPushes       int
	TotalValid        int
	AvgDiffScore      float64
	AvgResponseTimeMs int64
	BySchemaType      map[string]int
	ByResult          map[string]int
}

// Insights returns actionable insights from the log.
func (l *LearningLog) Insights() []string {
	stats := l.Stats()
	var insights []string

	// Push rate
	if stats.TotalOperations > 0 {
		pushRate := float64(stats.TotalPushes) / float64(stats.TotalOperations)
		if pushRate < 0.1 {
			insights = append(insights, "Low push rate (<10%) - schemas may be stable or thresholds too high")
		} else if pushRate > 0.9 {
			insights = append(insights, "High push rate (>90%) - content is changing frequently or thresholds too low")
		}
	}

	// Validation rate
	if stats.TotalOperations > 0 {
		validRate := float64(stats.TotalValid) / float64(stats.TotalOperations)
		if validRate < 0.9 {
			insights = append(insights, "Validation failure rate >10% - check schema generation logic")
		}
	}

	// Schema type distribution
	for schemaType, count := range stats.BySchemaType {
		if count > stats.TotalOperations/2 {
			insights = append(insights, "Schema type "+schemaType+" dominates - consider diversifying")
		}
	}

	// Response time
	if stats.AvgResponseTimeMs > 1000 {
		insights = append(insights, "High average response time (>1s) - check sidecar performance")
	}

	return insights
}

// Save persists the log to disk.
func (l *LearningLog) Save() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.filePath == "" {
		return nil
	}

	data, err := json.MarshalIndent(l.entries, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(l.filePath, data, 0644)
}

func (l *LearningLog) load() error {
	if l.filePath == "" {
		return nil
	}

	data, err := os.ReadFile(l.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	return json.Unmarshal(data, &l.entries)
}

// Clear removes all entries.
func (l *LearningLog) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.entries = make([]LearningEntry, 0)
}

// Export returns all entries as JSON.
func (l *LearningLog) Export() ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return json.MarshalIndent(l.entries, "", "  ")
}
