package seo

import (
	"container/heap"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

// JobQueue manages schema processing jobs.
type JobQueue struct {
	mu       sync.Mutex
	jobs     jobHeap
	pending  map[string]*SchemaJob
	dedupMap map[string]string // dedup key -> job ID
	filePath string
}

// NewJobQueue creates a job queue.
func NewJobQueue(filePath string) *JobQueue {
	q := &JobQueue{
		jobs:     make(jobHeap, 0),
		pending:  make(map[string]*SchemaJob),
		dedupMap: make(map[string]string),
		filePath: filePath,
	}

	heap.Init(&q.jobs)
	q.load()

	return q
}

// dedupKey generates a deduplication key for a job.
func (q *JobQueue) dedupKey(job *SchemaJob) string {
	schemaType := ""
	if job.Payload != nil {
		if st, ok := job.Payload["schemaType"].(string); ok {
			schemaType = st
		}
	}
	return job.SiteKey + ":" + job.URL + ":" + job.JobType + ":" + schemaType
}

// Enqueue adds a job to the queue.
// If a job with the same dedup key exists, it merges by bumping priority.
func (q *JobQueue) Enqueue(job *SchemaJob) string {
	q.mu.Lock()
	defer q.mu.Unlock()

	if job.JobID == "" {
		job.JobID = uuid.New().String()
	}
	if job.ScheduledAt.IsZero() {
		job.ScheduledAt = time.Now()
	}
	if job.MaxAttempts == 0 {
		job.MaxAttempts = 3
	}

	// Check for duplicate
	dedupKey := q.dedupKey(job)
	if existingID, ok := q.dedupMap[dedupKey]; ok {
		if existing, found := q.pending[existingID]; found {
			// Bump priority if new job has higher priority
			if job.Priority > existing.Priority {
				existing.Priority = job.Priority
			}
			// Reset schedule to now if requested sooner
			if job.ScheduledAt.Before(existing.ScheduledAt) {
				existing.ScheduledAt = job.ScheduledAt
			}
			return existingID // Return existing job ID
		}
	}

	heap.Push(&q.jobs, job)
	q.pending[job.JobID] = job
	q.dedupMap[dedupKey] = job.JobID

	return job.JobID
}

// EnqueueChange creates and enqueues a change detection job.
func (q *JobQueue) EnqueueChange(siteKey, url string, priority int) string {
	return q.Enqueue(&SchemaJob{
		SiteKey:  siteKey,
		URL:      url,
		JobType:  "detect-change",
		Priority: priority,
	})
}

// EnqueueClassify creates and enqueues a classification job.
func (q *JobQueue) EnqueueClassify(siteKey, url string, priority int) string {
	return q.Enqueue(&SchemaJob{
		SiteKey:  siteKey,
		URL:      url,
		JobType:  "classify-page",
		Priority: priority,
	})
}

// EnqueueGenerate creates and enqueues a generation job.
func (q *JobQueue) EnqueueGenerate(siteKey, url, schemaType string, priority int) string {
	return q.Enqueue(&SchemaJob{
		SiteKey:  siteKey,
		URL:      url,
		JobType:  "generate-schema",
		Priority: priority,
		Payload:  map[string]any{"schemaType": schemaType},
	})
}

// EnqueuePush creates and enqueues a push job.
func (q *JobQueue) EnqueuePush(siteKey, schemaKey, jsonld string, priority int) string {
	return q.Enqueue(&SchemaJob{
		SiteKey:  siteKey,
		JobType:  "push-schema",
		Priority: priority,
		Payload:  map[string]any{"schemaKey": schemaKey, "jsonld": jsonld},
	})
}

// Dequeue removes and returns the highest priority job.
func (q *JobQueue) Dequeue() *SchemaJob {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.jobs.Len() == 0 {
		return nil
	}

	job := heap.Pop(&q.jobs).(*SchemaJob)
	delete(q.pending, job.JobID)
	delete(q.dedupMap, q.dedupKey(job))

	return job
}

// Peek returns the next job without removing it.
func (q *JobQueue) Peek() *SchemaJob {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.jobs.Len() == 0 {
		return nil
	}

	return q.jobs[0]
}

// Requeue puts a failed job back in the queue.
func (q *JobQueue) Requeue(job *SchemaJob) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	job.Attempts++

	if job.Attempts >= job.MaxAttempts {
		return false // Max retries exceeded
	}

	// Exponential backoff
	delay := time.Duration(job.Attempts*job.Attempts) * time.Second
	job.ScheduledAt = time.Now().Add(delay)

	heap.Push(&q.jobs, job)
	q.pending[job.JobID] = job

	return true
}

// Get retrieves a job by ID.
func (q *JobQueue) Get(jobID string) *SchemaJob {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.pending[jobID]
}

// Cancel removes a job from the queue.
func (q *JobQueue) Cancel(jobID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	job, ok := q.pending[jobID]
	if !ok {
		return false
	}

	// Mark as cancelled (will be skipped when dequeued)
	job.MaxAttempts = 0
	delete(q.pending, jobID)

	return true
}

// Len returns the queue length.
func (q *JobQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.jobs.Len()
}

// Ready returns jobs ready to be processed.
func (q *JobQueue) Ready() []*SchemaJob {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	var ready []*SchemaJob

	for _, job := range q.jobs {
		if job.ScheduledAt.Before(now) || job.ScheduledAt.Equal(now) {
			ready = append(ready, job)
		}
	}

	return ready
}

// BySite returns jobs for a specific site.
func (q *JobQueue) BySite(siteKey string) []*SchemaJob {
	q.mu.Lock()
	defer q.mu.Unlock()

	var result []*SchemaJob
	for _, job := range q.pending {
		if job.SiteKey == siteKey {
			result = append(result, job)
		}
	}

	return result
}

// ByType returns jobs of a specific type.
func (q *JobQueue) ByType(jobType string) []*SchemaJob {
	q.mu.Lock()
	defer q.mu.Unlock()

	var result []*SchemaJob
	for _, job := range q.pending {
		if job.JobType == jobType {
			result = append(result, job)
		}
	}

	return result
}

// Clear removes all jobs.
func (q *JobQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.jobs = make(jobHeap, 0)
	q.pending = make(map[string]*SchemaJob)
	q.dedupMap = make(map[string]string)
	heap.Init(&q.jobs)
}

// Save persists the queue to disk.
func (q *JobQueue) Save() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.filePath == "" {
		return nil
	}

	jobs := make([]*SchemaJob, 0, len(q.pending))
	for _, job := range q.pending {
		jobs = append(jobs, job)
	}

	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(q.filePath, data, 0644)
}

func (q *JobQueue) load() error {
	if q.filePath == "" {
		return nil
	}

	data, err := os.ReadFile(q.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var jobs []*SchemaJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		return err
	}

	for _, job := range jobs {
		heap.Push(&q.jobs, job)
		q.pending[job.JobID] = job
	}

	return nil
}

// --- Priority heap implementation ---

type jobHeap []*SchemaJob

func (h jobHeap) Len() int { return len(h) }

func (h jobHeap) Less(i, j int) bool {
	// Higher priority first
	if h[i].Priority != h[j].Priority {
		return h[i].Priority > h[j].Priority
	}
	// Earlier scheduled first
	return h[i].ScheduledAt.Before(h[j].ScheduledAt)
}

func (h jobHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *jobHeap) Push(x any) {
	*h = append(*h, x.(*SchemaJob))
}

func (h *jobHeap) Pop() any {
	old := *h
	n := len(old)
	job := old[n-1]
	*h = old[0 : n-1]
	return job
}
