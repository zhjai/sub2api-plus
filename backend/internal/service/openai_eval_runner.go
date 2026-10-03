package service

import (
	"context"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	openAIEvalRunnerInterval = 30 * time.Second
	openAIEvalRunnerBatch    = 20
	openAIEvalRunnerWorkers  = 2
)

type OpenAIEvalRunner struct {
	repo    OpenAIEvalRepository
	service *OpenAIEvalService
	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
}

func NewOpenAIEvalRunner(repo OpenAIEvalRepository, service *OpenAIEvalService) *OpenAIEvalRunner {
	return &OpenAIEvalRunner{repo: repo, service: service}
}

func (r *OpenAIEvalRunner) Start() {
	if r == nil || r.repo == nil || r.service == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.done = make(chan struct{})
	go r.loop(ctx, r.done)
}

func (r *OpenAIEvalRunner) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.cancel = nil
	r.done = nil
	r.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		logger.LegacyPrintf("service.openai_eval_runner", "[OpenAIEvalRunner] stop timed out")
	}
}

func (r *OpenAIEvalRunner) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	// A long model evaluation must not delay quality snapshot refreshes.
	qualityDone := make(chan struct{})
	go r.qualityRefreshLoop(ctx, qualityDone)
	defer func() { <-qualityDone }()
	ticker := time.NewTicker(openAIEvalRunnerInterval)
	defer ticker.Stop()
	r.runDue(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runDue(ctx)
		}
	}
}

func (r *OpenAIEvalRunner) qualityRefreshLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(openAIEvalRunnerInterval)
	defer ticker.Stop()
	for {
		refreshCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		_, err := r.service.refreshOpenAIEvalQuality(refreshCtx, false)
		cancel()
		if err != nil && ctx.Err() == nil {
			logger.LegacyPrintf("service.openai_eval_runner", "[OpenAIEvalRunner] quality refresh failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *OpenAIEvalRunner) runDue(ctx context.Context) {
	items, err := r.repo.ClaimDueSchedules(ctx, time.Now().UTC(), openAIEvalRunnerBatch)
	if err != nil {
		if ctx.Err() == nil {
			logger.LegacyPrintf("service.openai_eval_runner", "[OpenAIEvalRunner] claim schedules failed: %v", err)
		}
		return
	}
	if len(items) == 0 {
		return
	}
	sem := make(chan struct{}, openAIEvalRunnerWorkers)
	var wg sync.WaitGroup
	for _, item := range items {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		wg.Add(1)
		go func(schedule OpenAIEvalScheduledRun) {
			defer wg.Done()
			defer func() { <-sem }()
			request := OpenAIEvalRunRequest{
				AccountID: schedule.AccountID, TestType: schedule.TestType,
				RequestedModel: schedule.RequestedModel, ReasoningEffort: schedule.ReasoningEffort,
				SampleMode: schedule.SampleMode, SampleCount: schedule.SampleCount,
			}
			if _, runErr := r.service.Run(ctx, request, 0, "scheduled"); runErr != nil && ctx.Err() == nil {
				logger.LegacyPrintf("service.openai_eval_runner", "[OpenAIEvalRunner] scheduled run failed account=%d model=%s type=%s: %v", schedule.AccountID, schedule.RequestedModel, schedule.TestType, runErr)
			}
		}(item)
	}
	wg.Wait()
}
