package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	events "github.com/syndg/deck/internal/services/events"
)

// Scheduler manages dependency-aware stream execution.
type Scheduler struct {
	streams       *db.StreamStore
	plans         *db.PlanStore
	maxConcurrent int
	mu            sync.Mutex
	activeStreams map[string]bool // streamID → executing
	eventBus      *events.PersistentBus
	logger        *slog.Logger
}

// NewScheduler creates a new Scheduler.
func NewScheduler(
	streams *db.StreamStore,
	plans *db.PlanStore,
	maxConcurrent int,
	eventBus *events.PersistentBus,
	logger *slog.Logger,
) *Scheduler {
	return &Scheduler{
		streams:       streams,
		plans:         plans,
		maxConcurrent: maxConcurrent,
		activeStreams: make(map[string]bool),
		eventBus:      eventBus,
		logger:        logger,
	}
}

// GetReadyStreams returns streams that are ready to execute:
//   - Status is "pending"
//   - All dependency streams have status "completed"
//   - Total active streams is under maxConcurrent
//
// Uses StreamStore.ListReady() for dependency resolution, then caps by concurrency limit.
func (s *Scheduler) GetReadyStreams(ctx context.Context, planID string) ([]domain.Stream, error) {
	ready, err := s.streams.ListReady(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("listing ready streams for plan %s: %w", planID, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	available := s.maxConcurrent - len(s.activeStreams)
	if available <= 0 {
		return nil, nil
	}

	// Filter out streams already tracked as active (e.g. already dispatched).
	var candidates []domain.Stream
	for _, st := range ready {
		if !s.activeStreams[st.ID] {
			candidates = append(candidates, st)
		}
	}

	if len(candidates) > available {
		candidates = candidates[:available]
	}
	return candidates, nil
}

// MarkExecuting marks a stream as actively executing and tracks it.
// Updates stream status to "executing" via StreamStore.UpdateStatus().
func (s *Scheduler) MarkExecuting(ctx context.Context, streamID string) error {
	if err := s.streams.UpdateStatus(ctx, streamID, "executing"); err != nil {
		return fmt.Errorf("updating stream %s to executing: %w", streamID, err)
	}

	s.mu.Lock()
	s.activeStreams[streamID] = true
	s.mu.Unlock()

	s.logger.Info("stream executing", "stream_id", streamID)
	return nil
}

// MarkCompleted marks a stream as completed and removes from active set.
// 1. Update stream status to "completed" (unless already merge_ready)
// 2. Remove from activeStreams map
// 3. Check for newly unblocked streams in the same plan
// 4. Publish EventStreamReady for each newly ready stream
func (s *Scheduler) MarkCompleted(ctx context.Context, streamID string, planID string) error {
	// Don't downgrade merge_ready back to completed — the stream was already
	// signaled for merge by signal_merge_ready inside its sub-execution.
	current, err := s.streams.Get(ctx, streamID)
	if err != nil || (current.Status != "merge_ready" && current.Status != "merged") {
		if err := s.streams.UpdateStatus(ctx, streamID, "completed"); err != nil {
			return fmt.Errorf("updating stream %s to completed: %w", streamID, err)
		}
	}

	s.mu.Lock()
	delete(s.activeStreams, streamID)
	s.mu.Unlock()

	s.logger.Info("stream completed", "stream_id", streamID)

	// Check for newly unblocked streams and publish EventStreamReady for each.
	newly, err := s.streams.ListReady(ctx, planID)
	if err != nil {
		return fmt.Errorf("listing ready streams after completion of %s: %w", streamID, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, st := range newly {
		if s.activeStreams[st.ID] {
			continue
		}
		s.eventBus.Emit(domain.EventStreamReady, "", st.ID, "",
			"stream_id", st.ID,
			"plan_id", planID,
		)
		s.logger.Info("stream ready cascade", "stream_id", st.ID, "plan_id", planID)
	}

	return nil
}

// MarkFailed marks a stream as failed and removes from active set.
func (s *Scheduler) MarkFailed(ctx context.Context, streamID string) error {
	if err := s.streams.UpdateStatus(ctx, streamID, "failed"); err != nil {
		return fmt.Errorf("updating stream %s to failed: %w", streamID, err)
	}

	s.mu.Lock()
	delete(s.activeStreams, streamID)
	s.mu.Unlock()

	s.logger.Info("stream failed", "stream_id", streamID)
	return nil
}

// ActiveCount returns the number of currently executing streams.
func (s *Scheduler) ActiveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.activeStreams)
}

// CanScheduleMore returns true if under the concurrent stream limit.
func (s *Scheduler) CanScheduleMore() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.activeStreams) < s.maxConcurrent
}
