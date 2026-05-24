package seo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

// DiffGate decides whether a schema update should be pushed.
type DiffGate struct {
	registry *SiteRegistry
	cache    *StateCache
	client   *Client
}

// NewDiffGate creates a diff gate.
func NewDiffGate(registry *SiteRegistry, cache *StateCache, client *Client) *DiffGate {
	return &DiffGate{
		registry: registry,
		cache:    cache,
		client:   client,
	}
}

// Compare evaluates whether a candidate should replace the current schema.
func (g *DiffGate) Compare(candidate *SchemaCandidate) *SchemaDecision {
	decision := &SchemaDecision{
		ShouldPush: false,
		Reason:     "",
	}

	// Get current schema from cache
	cached := g.cache.Get(candidate.URL)

	// If no cached version, always push
	if cached == nil {
		decision.ShouldPush = true
		decision.Reason = "no existing schema"
		decision.DiffScore = 1.0
		return decision
	}

	// Check cooldown
	site := g.registry.Get(candidate.SiteKey)
	if site != nil && site.PushPolicy.CooldownMinutes > 0 {
		cooldown := time.Duration(site.PushPolicy.CooldownMinutes) * time.Minute
		if time.Since(cached.LastPushTime) < cooldown {
			decision.ShouldPush = false
			decision.Reason = "cooldown period not elapsed"
			return decision
		}
	}

	// Compare schema hashes
	candidateHash := hashSchema(candidate.JSONLD)
	if candidateHash == cached.LastSchemaHash {
		decision.ShouldPush = false
		decision.Reason = "schema unchanged"
		decision.DiffScore = 0.0
		return decision
	}

	// Calculate structural diff score
	diffScore := g.calculateDiffScore(cached.LastSchemaHash, candidate.JSONLD)
	decision.DiffScore = diffScore
	decision.PreviousVersion = cached.LastSchemaHash

	// Check threshold
	minScore := 0.05 // default
	if site != nil && site.PushPolicy.MinDiffScore > 0 {
		minScore = site.PushPolicy.MinDiffScore
	}

	if diffScore < minScore {
		decision.ShouldPush = false
		decision.Reason = "diff below threshold"
		return decision
	}

	// Check for meaningful changes
	if g.isMeaningfulChange(candidate) {
		decision.ShouldPush = true
		decision.Reason = "meaningful structural change"
		return decision
	}

	decision.ShouldPush = true
	decision.Reason = "diff score exceeds threshold"
	return decision
}

func (g *DiffGate) calculateDiffScore(previousHash, newJSONLD string) float64 {
	// Fetch previous schema from sidecar if needed
	// For now, use a simplified structural comparison

	newNormalized := normalizeForComparison(newJSONLD)
	newHash := hashSchema(newNormalized)

	if previousHash == newHash {
		return 0.0
	}

	// Parse and compare structure
	var newSchema map[string]any
	if err := json.Unmarshal([]byte(newJSONLD), &newSchema); err != nil {
		return 1.0 // Can't parse, treat as fully changed
	}

	// Count fields to estimate change magnitude
	fieldCount := countFields(newSchema)

	// Heuristic: more fields = more likely to be a meaningful change
	// This is simplified; real implementation would compare field-by-field
	if fieldCount > 10 {
		return 0.6 // Larger schema, moderate change assumed
	}
	return 0.4 // Smaller schema, minor change assumed
}

func (g *DiffGate) isMeaningfulChange(candidate *SchemaCandidate) bool {
	var schema map[string]any
	if err := json.Unmarshal([]byte(candidate.JSONLD), &schema); err != nil {
		return false
	}

	// New schema type is always meaningful
	schemaType, _ := schema["@type"].(string)
	cached := g.cache.Get(candidate.URL)
	if cached == nil {
		return true
	}

	// Check for structural changes that matter
	meaningfulFields := []string{
		"mainEntity",
		"@graph",
		"author",
		"offers",
		"aggregateRating",
		"review",
	}

	for _, field := range meaningfulFields {
		if _, ok := schema[field]; ok {
			return true
		}
	}

	// New FAQ items
	if schemaType == "FAQPage" {
		if mainEntity, ok := schema["mainEntity"].([]any); ok {
			if len(mainEntity) > 0 {
				return true
			}
		}
	}

	return false
}

func hashSchema(jsonld string) string {
	normalized := normalizeForComparison(jsonld)
	hash := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(hash[:])
}

func normalizeForComparison(jsonld string) string {
	var parsed any
	if err := json.Unmarshal([]byte(jsonld), &parsed); err != nil {
		return jsonld
	}

	// Re-marshal with sorted keys
	normalized := sortedMarshal(parsed)
	return normalized
}

func sortedMarshal(v any) string {
	switch val := v.(type) {
	case map[string]any:
		// Sort keys
		keys := make([]string, 0, len(val))
		for k := range val {
			// Skip timestamp fields for comparison
			if k == "dateModified" || k == "updated_at" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)

		result := "{"
		for i, k := range keys {
			if i > 0 {
				result += ","
			}
			result += `"` + k + `":` + sortedMarshal(val[k])
		}
		result += "}"
		return result

	case []any:
		result := "["
		for i, item := range val {
			if i > 0 {
				result += ","
			}
			result += sortedMarshal(item)
		}
		result += "]"
		return result

	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func countFields(schema map[string]any) int {
	count := 0
	for _, v := range schema {
		count++
		if nested, ok := v.(map[string]any); ok {
			count += countFields(nested)
		}
		if arr, ok := v.([]any); ok {
			for _, item := range arr {
				if nested, ok := item.(map[string]any); ok {
					count += countFields(nested)
				}
			}
		}
	}
	return count
}

// ShouldPushImmediate checks if a change warrants immediate push.
func ShouldPushImmediate(candidate *SchemaCandidate) bool {
	var schema map[string]any
	if err := json.Unmarshal([]byte(candidate.JSONLD), &schema); err != nil {
		return false
	}

	// Events with upcoming dates
	if schema["@type"] == "Event" {
		return true
	}

	// New schema types
	schemaType, _ := schema["@type"].(string)
	highPriorityTypes := []string{"FAQPage", "Product", "Event", "JobPosting"}
	for _, t := range highPriorityTypes {
		if schemaType == t {
			return true
		}
	}

	return false
}
