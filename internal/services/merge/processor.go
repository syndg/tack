package merge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/syndg/deck/internal/db"
	"github.com/syndg/deck/internal/domain"
	"github.com/syndg/deck/internal/harness/gates"
	"github.com/syndg/deck/internal/sandbox"
	events "github.com/syndg/deck/internal/services/events"
)

// Processor manages the merge queue lifecycle.
type Processor struct {
	queue       *db.MergeQueueStore
	streams     *db.StreamStore
	plans       *db.PlanStore
	objectives  *db.ObjectiveStore
	merger      *GitMerger
	differ      *DiffExtractor
	gateRunner  *gates.Runner
	sandboxProv sandbox.SandboxProvider
	eventBus    *events.PersistentBus
	logger      *slog.Logger

	mu         sync.Mutex
	processing bool
	cancel     context.CancelFunc
}

// NewProcessor creates a new merge queue Processor.
func NewProcessor(
	queue *db.MergeQueueStore,
	streams *db.StreamStore,
	plans *db.PlanStore,
	objectives *db.ObjectiveStore,
	merger *GitMerger,
	differ *DiffExtractor,
	gateRunner *gates.Runner,
	sandboxProv sandbox.SandboxProvider,
	eventBus *events.PersistentBus,
	logger *slog.Logger,
) *Processor {
	return &Processor{
		queue:       queue,
		streams:     streams,
		plans:       plans,
		objectives:  objectives,
		merger:      merger,
		differ:      differ,
		gateRunner:  gateRunner,
		sandboxProv: sandboxProv,
		eventBus:    eventBus,
		logger:      logger.With("component", "merge-processor"),
	}
}

// Start begins the merge processor loop.
// Subscribes to EventMergeQueued to trigger processing.
// Also runs on a ticker (every 30s) to catch any missed events.
func (p *Processor) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel

	sub, unsub := p.eventBus.Subscribe(32)

	go func() {
		defer unsub()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-sub:
				if !ok {
					return
				}
				if event.Type == domain.EventMergeQueued {
					p.processAll(ctx)
				}
			case <-ticker.C:
				p.processAll(ctx)
			}
		}
	}()

	p.logger.Info("merge processor started")
	return nil
}

// Stop cancels the processor loop.
func (p *Processor) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.logger.Info("merge processor stopped")
}

// ProcessNext dequeues and processes the next pending merge entry.
// Returns true if an entry was processed, false if queue was empty
// or all pending entries have unsatisfied dependencies.
func (p *Processor) ProcessNext(ctx context.Context) (bool, error) {
	entries, err := p.queue.ListPending(ctx)
	if err != nil {
		return false, fmt.Errorf("listing pending entries: %w", err)
	}
	if len(entries) == 0 {
		return false, nil
	}

	// Find first entry with satisfied dependencies.
	var entry *domain.MergeEntry
	for i := range entries {
		satisfied, err := p.checkDependencies(ctx, &entries[i])
		if err != nil {
			p.logger.Warn("checking dependencies", "entry", entries[i].ID, "error", err)
			continue
		}
		if satisfied {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		return false, nil
	}

	p.processEntry(ctx, entry)
	return true, nil
}

// EnqueueStream creates a merge entry for a stream that has signaled merge_ready.
// Called by the merge_queue deterministic handler.
func (p *Processor) EnqueueStream(ctx context.Context, streamID string) error {
	// 1. Look up the stream to get plan_id.
	stream, err := p.streams.Get(ctx, streamID)
	if err != nil {
		return fmt.Errorf("getting stream: %w", err)
	}

	// 2. Look up the plan to get objective_id.
	plan, err := p.plans.Get(ctx, stream.PlanID)
	if err != nil {
		return fmt.Errorf("getting plan: %w", err)
	}

	// 3. Determine branch name from the stream's sandbox.
	branch, err := p.getStreamBranch(ctx, streamID)
	if err != nil {
		return fmt.Errorf("getting stream branch: %w", err)
	}

	// 4. Create MergeEntry with status "pending" and enqueue.
	entry := &domain.MergeEntry{
		StreamID:    streamID,
		PlanID:      stream.PlanID,
		ObjectiveID: plan.ObjectiveID,
		Branch:      branch,
		Status:      domain.MergeStatusPending,
	}
	if err := p.queue.Enqueue(ctx, entry); err != nil {
		return fmt.Errorf("enqueuing merge entry: %w", err)
	}

	// 5. Publish EventMergeQueued.
	payload, _ := json.Marshal(map[string]string{
		"entry_id":     entry.ID,
		"stream_id":    streamID,
		"plan_id":      stream.PlanID,
		"objective_id": plan.ObjectiveID,
		"branch":       branch,
	})
	p.eventBus.Publish(domain.Event{
		Type:      domain.EventMergeQueued,
		Objective: plan.ObjectiveID,
		Stream:    streamID,
		Payload:   string(payload),
		CreatedAt: time.Now(),
	})

	p.logger.Info("stream enqueued for merge",
		"stream_id", streamID,
		"entry_id", entry.ID,
		"branch", branch,
	)

	return nil
}

// processAll drains the pending queue, processing entries one at a time.
// Uses a mutex to prevent concurrent processing runs.
func (p *Processor) processAll(ctx context.Context) {
	p.mu.Lock()
	if p.processing {
		p.mu.Unlock()
		return
	}
	p.processing = true
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.processing = false
		p.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		processed, err := p.ProcessNext(ctx)
		if err != nil {
			p.logger.Error("processing merge entry", "error", err)
			return
		}
		if !processed {
			return
		}
	}
}

// processEntry handles the full lifecycle of a single merge entry.
func (p *Processor) processEntry(ctx context.Context, entry *domain.MergeEntry) {
	p.logger.Info("processing merge entry",
		"entry", entry.ID,
		"stream", entry.StreamID,
		"branch", entry.Branch,
	)

	// Update entry status to "merging".
	if err := p.queue.UpdateStatus(ctx, entry.ID, domain.MergeStatusMerging, entry.Tier, "", ""); err != nil {
		p.logger.Error("updating entry to merging", "entry", entry.ID, "error", err)
		return
	}

	// Update stream status to "merging".
	if err := p.streams.UpdateStatus(ctx, entry.StreamID, domain.StreamStatusMerging); err != nil {
		p.logger.Error("updating stream to merging", "stream", entry.StreamID, "error", err)
	}

	// Get or create sandbox for merge operations.
	sb, err := p.getMergerSandbox(ctx, entry.ObjectiveID)
	if err != nil {
		errMsg := fmt.Sprintf("getting merger sandbox: %s", err)
		p.logger.Error(errMsg)
		p.failEntry(ctx, entry, 0, errMsg)
		return
	}

	// Attempt merge via GitMerger (tries tiers 1→2→3 sequentially).
	result, err := p.merger.Merge(ctx, sb, entry.Branch)
	if err != nil {
		errMsg := fmt.Sprintf("merge operation: %s", err)
		p.logger.Error(errMsg)
		p.failEntry(ctx, entry, 0, errMsg)
		return
	}

	if result.Success {
		p.handleMergeSuccess(ctx, sb, entry, result)
	} else {
		// All tiers exhausted — mark as conflict.
		if err := p.queue.UpdateStatus(ctx, entry.ID, domain.MergeStatusConflict, result.Tier, result.Error, ""); err != nil {
			p.logger.Error("updating entry to conflict", "entry", entry.ID, "error", err)
		}
		p.publishMergeFailed(entry, result.Error)
		p.logger.Warn("merge conflict, all tiers exhausted",
			"entry", entry.ID,
			"stream", entry.StreamID,
			"branch", entry.Branch,
			"tier", result.Tier,
			"conflicts", result.Conflicts,
		)
	}

	// Check if all streams for the objective are merged.
	if err := p.checkObjectiveComplete(ctx, entry.ObjectiveID); err != nil {
		p.logger.Error("checking objective completion", "objective", entry.ObjectiveID, "error", err)
	}
}

// handleMergeSuccess runs post-merge gates and finalizes a successful merge.
func (p *Processor) handleMergeSuccess(ctx context.Context, sb sandbox.Sandbox, entry *domain.MergeEntry, result *MergeResult) {
	// Run post-merge quality gates.
	gateResult, err := p.runPostMergeGates(ctx, sb, entry.PlanID)
	if err != nil {
		p.revertMerge(ctx, sb, entry)
		errMsg := fmt.Sprintf("running post-merge gates: %s", err)
		p.failEntry(ctx, entry, result.Tier, errMsg)
		return
	}

	if gateResult != nil && !gateResult.AllPassed {
		p.revertMerge(ctx, sb, entry)
		p.failEntry(ctx, entry, result.Tier, "post-merge quality gates failed")
		return
	}

	// Extract diff summary.
	diffJSON, err := p.extractMergedDiffJSON(ctx, sb)
	if err != nil {
		p.logger.Warn("extracting diff after merge", "error", err)
		// Fallback: build minimal diff from merge result.
		fallback, _ := json.Marshal(map[string]int{
			"files_changed": result.FilesChanged,
			"insertions":    result.Insertions,
			"deletions":     result.Deletions,
		})
		diffJSON = string(fallback)
	}

	// Mark entry as merged.
	if err := p.queue.UpdateStatus(ctx, entry.ID, domain.MergeStatusMerged, result.Tier, "", diffJSON); err != nil {
		p.logger.Error("updating entry to merged", "entry", entry.ID, "error", err)
	}

	// Mark stream as merged.
	if err := p.streams.UpdateStatus(ctx, entry.StreamID, domain.StreamStatusMerged); err != nil {
		p.logger.Error("updating stream to merged", "stream", entry.StreamID, "error", err)
	}

	p.publishMergeCompleted(entry, result)

	p.logger.Info("merge completed",
		"entry", entry.ID,
		"stream", entry.StreamID,
		"branch", entry.Branch,
		"tier", result.Tier,
		"files_changed", result.FilesChanged,
	)
}

// extractMergedDiffJSON prefers ORIG_HEAD..HEAD so the stored diff covers the
// full merged delta, including fast-forward and multi-commit merges. Falls back
// to HEAD~1 for older git state/tests.
func (p *Processor) extractMergedDiffJSON(ctx context.Context, sb sandbox.Sandbox) (string, error) {
	type diffRefPair struct {
		base string
		head string
	}

	for _, refs := range []diffRefPair{{base: "ORIG_HEAD", head: "HEAD"}, {base: "HEAD~1", head: "HEAD"}} {
		diffJSON, err := p.differ.ExtractJSON(ctx, sb, refs.base, refs.head)
		if err == nil {
			return diffJSON, nil
		}
		p.logger.Warn("extract diff attempt failed", "base", refs.base, "head", refs.head, "error", err)
	}

	return "", fmt.Errorf("unable to extract merged diff")
}

// revertMerge undoes the last merge in the sandbox.
func (p *Processor) revertMerge(ctx context.Context, sb sandbox.Sandbox, entry *domain.MergeEntry) {
	var lastErr error
	for _, cmd := range []string{"git reset --hard ORIG_HEAD", "git reset --hard HEAD~1"} {
		if _, err := sb.Exec(ctx, cmd, sandbox.ExecOpts{}); err == nil {
			return
		} else {
			lastErr = err
		}
	}
	p.logger.Error("reverting merge", "entry", entry.ID, "error", lastErr)
}

// failEntry marks a merge entry as failed and publishes the failure event.
func (p *Processor) failEntry(ctx context.Context, entry *domain.MergeEntry, tier int, errMsg string) {
	if err := p.queue.UpdateStatus(ctx, entry.ID, domain.MergeStatusFailed, tier, errMsg, ""); err != nil {
		p.logger.Error("updating entry to failed", "entry", entry.ID, "error", err)
	}
	p.publishMergeFailed(entry, errMsg)
}

// getStreamBranch determines the git branch name for a stream by querying its sandbox.
func (p *Processor) getStreamBranch(ctx context.Context, streamID string) (string, error) {
	sandboxes, err := p.sandboxProv.List(ctx, map[string]string{
		"deck.stream": streamID,
	})
	if err != nil {
		return "", fmt.Errorf("listing sandboxes for stream: %w", err)
	}
	if len(sandboxes) == 0 {
		return "", fmt.Errorf("no sandbox found for stream %s", streamID)
	}

	sb := sandboxes[0]
	res, err := sb.Exec(ctx, "git rev-parse --abbrev-ref HEAD", sandbox.ExecOpts{})
	if err != nil {
		return "", fmt.Errorf("getting branch from sandbox: %w", err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("git rev-parse failed (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	branch := strings.TrimSpace(res.Stdout)
	if branch == "" {
		return "", fmt.Errorf("empty branch name from sandbox %s", sb.ID())
	}
	return branch, nil
}

// getMergerSandbox returns an existing merger sandbox for the objective,
// or creates a new one if none exists.
func (p *Processor) getMergerSandbox(ctx context.Context, objectiveID string) (sandbox.Sandbox, error) {
	// Try to find an existing merger sandbox.
	sandboxes, err := p.sandboxProv.List(ctx, map[string]string{
		"deck.objective": objectiveID,
		"deck.role":      "merger",
	})
	if err == nil && len(sandboxes) > 0 {
		return sandboxes[0], nil
	}

	// Create a new merger sandbox.
	objShort := objectiveID
	if len(objShort) > 8 {
		objShort = objShort[:8]
	}

	sb, err := p.sandboxProv.Create(ctx, sandbox.CreateOpts{
		Name: fmt.Sprintf("deck-merger-%s", objShort),
		Labels: map[string]string{
			"deck.objective": objectiveID,
			"deck.role":      "merger",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("creating merger sandbox: %w", err)
	}
	return sb, nil
}

// checkDependencies verifies that all dependency streams for this stream
// have already been merged. Returns true if all deps are satisfied.
func (p *Processor) checkDependencies(ctx context.Context, entry *domain.MergeEntry) (bool, error) {
	stream, err := p.streams.Get(ctx, entry.StreamID)
	if err != nil {
		return false, fmt.Errorf("getting stream: %w", err)
	}

	if len(stream.Dependencies) == 0 {
		return true, nil
	}

	for _, depID := range stream.Dependencies {
		depEntry, err := p.queue.GetByStream(ctx, depID)
		if err != nil {
			// No merge entry for the dependency yet — not satisfied.
			return false, nil
		}
		if depEntry.Status != domain.MergeStatusMerged {
			return false, nil
		}
	}

	return true, nil
}

// runPostMergeGates runs quality gates after a successful merge.
// Uses the plan's quality gates configuration.
func (p *Processor) runPostMergeGates(ctx context.Context, sb sandbox.Sandbox, planID string) (*gates.RunResult, error) {
	plan, err := p.plans.Get(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("getting plan: %w", err)
	}

	if len(plan.QualityGates) == 0 {
		return nil, nil
	}

	gateList := make([]gates.Gate, len(plan.QualityGates))
	for i, cmd := range plan.QualityGates {
		gateList[i] = gates.Gate{
			Name:    fmt.Sprintf("post-merge-gate-%d", i+1),
			Command: cmd,
		}
	}

	return p.gateRunner.Run(ctx, sb, gateList, false)
}

// checkObjectiveComplete checks if all streams for an objective have been merged.
// If so, transitions the objective to "reviewing" status.
func (p *Processor) checkObjectiveComplete(ctx context.Context, objectiveID string) error {
	plan, err := p.plans.GetByObjective(ctx, objectiveID)
	if err != nil {
		return fmt.Errorf("getting plan for objective: %w", err)
	}

	streams, err := p.streams.ListByPlan(ctx, plan.ID)
	if err != nil {
		return fmt.Errorf("listing streams for plan: %w", err)
	}

	if len(streams) == 0 {
		return nil
	}

	for _, s := range streams {
		if s.Status != domain.StreamStatusMerged {
			return nil
		}
	}

	// All streams merged — check if objective still needs transitioning.
	obj, err := p.objectives.Get(ctx, objectiveID)
	if err != nil {
		return fmt.Errorf("getting objective: %w", err)
	}

	// Only transition from executing → reviewing.
	if obj.Status != domain.ObjectiveStatusExecuting {
		return nil
	}

	if err := p.objectives.UpdateStatus(ctx, objectiveID, domain.ObjectiveStatusReviewing); err != nil {
		return fmt.Errorf("updating objective to reviewing: %w", err)
	}

	payload, _ := json.Marshal(map[string]string{
		"from": string(domain.ObjectiveStatusExecuting),
		"to":   string(domain.ObjectiveStatusReviewing),
	})
	p.eventBus.Publish(domain.Event{
		Type:      domain.EventObjectiveUpdated,
		Objective: objectiveID,
		Payload:   string(payload),
		CreatedAt: time.Now(),
	})

	p.logger.Info("all streams merged, objective transitioned to reviewing",
		"objective_id", objectiveID,
	)

	return nil
}

// publishMergeCompleted publishes an EventMergeCompleted event.
func (p *Processor) publishMergeCompleted(entry *domain.MergeEntry, result *MergeResult) {
	payload, _ := json.Marshal(map[string]any{
		"entry_id":      entry.ID,
		"stream_id":     entry.StreamID,
		"branch":        entry.Branch,
		"tier":          result.Tier,
		"files_changed": result.FilesChanged,
		"insertions":    result.Insertions,
		"deletions":     result.Deletions,
	})
	p.eventBus.Publish(domain.Event{
		Type:      domain.EventMergeCompleted,
		Objective: entry.ObjectiveID,
		Stream:    entry.StreamID,
		Payload:   string(payload),
		CreatedAt: time.Now(),
	})
}

// publishMergeFailed publishes an EventMergeFailed event.
func (p *Processor) publishMergeFailed(entry *domain.MergeEntry, errMsg string) {
	payload, _ := json.Marshal(map[string]string{
		"entry_id":  entry.ID,
		"stream_id": entry.StreamID,
		"branch":    entry.Branch,
		"error":     errMsg,
	})
	p.eventBus.Publish(domain.Event{
		Type:      domain.EventMergeFailed,
		Objective: entry.ObjectiveID,
		Stream:    entry.StreamID,
		Payload:   string(payload),
		CreatedAt: time.Now(),
	})
}
