package seo

import (
	"sync"
	"time"
)

// Throttle controls push rate per site.
type Throttle struct {
	mu            sync.Mutex
	pushHistory   map[string][]time.Time // key: siteKey
	config        ThrottleConfig
}

// ThrottleConfig defines throttling behavior.
type ThrottleConfig struct {
	MaxPushesPerHour     int           // Maximum pushes per site per hour
	MinIntervalBetween   time.Duration // Minimum time between pushes for same site
	BurstAllowance       int           // Extra pushes allowed in burst
	BurstRecoveryMinutes int           // Minutes to recover one burst token
}

// DefaultThrottleConfig returns sensible defaults.
func DefaultThrottleConfig() ThrottleConfig {
	return ThrottleConfig{
		MaxPushesPerHour:     10,
		MinIntervalBetween:   5 * time.Minute,
		BurstAllowance:       3,
		BurstRecoveryMinutes: 20,
	}
}

// NewThrottle creates a throttle with the given config.
func NewThrottle(config ThrottleConfig) *Throttle {
	return &Throttle{
		pushHistory: make(map[string][]time.Time),
		config:      config,
	}
}

// Allow checks if a push is allowed for the given site.
func (t *Throttle) Allow(siteKey string) (bool, string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	t.cleanOldHistory(siteKey, now)

	history := t.pushHistory[siteKey]

	// Check minimum interval
	if len(history) > 0 {
		lastPush := history[len(history)-1]
		if now.Sub(lastPush) < t.config.MinIntervalBetween {
			remaining := t.config.MinIntervalBetween - now.Sub(lastPush)
			return false, "too soon since last push, wait " + remaining.Round(time.Second).String()
		}
	}

	// Check hourly limit
	hourAgo := now.Add(-time.Hour)
	recentCount := 0
	for _, ts := range history {
		if ts.After(hourAgo) {
			recentCount++
		}
	}

	// Allow burst
	effectiveLimit := t.config.MaxPushesPerHour + t.calculateBurstTokens(siteKey, now)

	if recentCount >= effectiveLimit {
		return false, "hourly limit reached"
	}

	return true, ""
}

// Record records a push for rate limiting.
func (t *Throttle) Record(siteKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	t.cleanOldHistory(siteKey, now)

	t.pushHistory[siteKey] = append(t.pushHistory[siteKey], now)
}

// AllowAndRecord combines Allow and Record atomically.
func (t *Throttle) AllowAndRecord(siteKey string) (bool, string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	t.cleanOldHistory(siteKey, now)

	history := t.pushHistory[siteKey]

	// Check minimum interval
	if len(history) > 0 {
		lastPush := history[len(history)-1]
		if now.Sub(lastPush) < t.config.MinIntervalBetween {
			remaining := t.config.MinIntervalBetween - now.Sub(lastPush)
			return false, "too soon since last push, wait " + remaining.Round(time.Second).String()
		}
	}

	// Check hourly limit
	hourAgo := now.Add(-time.Hour)
	recentCount := 0
	for _, ts := range history {
		if ts.After(hourAgo) {
			recentCount++
		}
	}

	effectiveLimit := t.config.MaxPushesPerHour + t.calculateBurstTokens(siteKey, now)

	if recentCount >= effectiveLimit {
		return false, "hourly limit reached"
	}

	// Record the push
	t.pushHistory[siteKey] = append(t.pushHistory[siteKey], now)

	return true, ""
}

// GetStats returns throttle statistics for a site.
func (t *Throttle) GetStats(siteKey string) ThrottleStats {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	t.cleanOldHistory(siteKey, now)

	history := t.pushHistory[siteKey]

	stats := ThrottleStats{
		SiteKey:          siteKey,
		TotalPushes:      len(history),
		HourlyLimit:      t.config.MaxPushesPerHour,
		MinInterval:      t.config.MinIntervalBetween,
	}

	// Count recent pushes
	hourAgo := now.Add(-time.Hour)
	for _, ts := range history {
		if ts.After(hourAgo) {
			stats.PushesThisHour++
		}
	}

	stats.RemainingThisHour = t.config.MaxPushesPerHour - stats.PushesThisHour
	if stats.RemainingThisHour < 0 {
		stats.RemainingThisHour = 0
	}

	// Time until next allowed
	if len(history) > 0 {
		lastPush := history[len(history)-1]
		nextAllowed := lastPush.Add(t.config.MinIntervalBetween)
		if nextAllowed.After(now) {
			stats.NextAllowedIn = nextAllowed.Sub(now)
		}
	}

	return stats
}

// ThrottleStats contains throttle statistics.
type ThrottleStats struct {
	SiteKey          string
	TotalPushes      int
	PushesThisHour   int
	RemainingThisHour int
	HourlyLimit      int
	MinInterval      time.Duration
	NextAllowedIn    time.Duration
}

func (t *Throttle) cleanOldHistory(siteKey string, now time.Time) {
	history := t.pushHistory[siteKey]
	if len(history) == 0 {
		return
	}

	// Keep only last 24 hours
	cutoff := now.Add(-24 * time.Hour)
	newHistory := make([]time.Time, 0, len(history))
	for _, ts := range history {
		if ts.After(cutoff) {
			newHistory = append(newHistory, ts)
		}
	}

	t.pushHistory[siteKey] = newHistory
}

func (t *Throttle) calculateBurstTokens(siteKey string, now time.Time) int {
	if t.config.BurstAllowance == 0 {
		return 0
	}

	history := t.pushHistory[siteKey]
	if len(history) == 0 {
		return t.config.BurstAllowance
	}

	// Calculate recovery based on time since last push
	lastPush := history[len(history)-1]
	minutesSince := int(now.Sub(lastPush).Minutes())

	if t.config.BurstRecoveryMinutes == 0 {
		return 0
	}

	recovered := minutesSince / t.config.BurstRecoveryMinutes
	if recovered > t.config.BurstAllowance {
		recovered = t.config.BurstAllowance
	}

	return recovered
}

// Reset clears throttle history for a site.
func (t *Throttle) Reset(siteKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.pushHistory, siteKey)
}

// ResetAll clears all throttle history.
func (t *Throttle) ResetAll() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.pushHistory = make(map[string][]time.Time)
}
