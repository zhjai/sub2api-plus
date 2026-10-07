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

type openAIEvalAdmissionTurnKey struct{}

func openAIEvalAdmissionFinished(ctx context.Context) {
	if done, ok := ctx.Value(openAIEvalAdmissionTurnKey{}).(func()); ok {
		done()
	}
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
	completion, completionBased := r.repo.(OpenAIEvalCompletionRepository)
	var items []OpenAIEvalScheduledRun
	var err error
	if completionBased {
		items, err = completion.ClaimDueSchedulesForCompletion(ctx, time.Now().UTC(), openAIEvalRunnerBatch)
	} else {
		items, err = r.repo.ClaimDueSchedules(ctx, time.Now().UTC(), openAIEvalRunnerBatch)
	}
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
	previous := make(chan struct{})
	close(previous)
	var wg sync.WaitGroup
	for _, item := range items {
		turn, next := previous, make(chan struct{})
		previous = next
		wg.Add(1)
		go func(schedule OpenAIEvalScheduledRun) {
			defer wg.Done()
			var once sync.Once
			signalNext := func() { once.Do(func() { close(next) }) }
			defer signalNext()
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			if completionBased {
				done := make(chan struct{})
				go func() {
					defer close(done)
					ticker := time.NewTicker(30 * time.Second)
					defer ticker.Stop()
					for {
						select {
						case <-runCtx.Done():
							return
						case <-ticker.C:
							renewCtx, renewCancel := context.WithTimeout(runCtx, 5*time.Second)
							ok, err := completion.RenewScheduleClaim(renewCtx, schedule)
							renewCancel()
							if err != nil || !ok {
								cancel()
								return
							}
						}
					}
				}()
				defer func() {
					cancel()
					<-done
					finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer finishCancel()
					if err := completion.CompleteSchedule(finishCtx, schedule, time.Now().UTC()); err != nil {
						logger.LegacyPrintf("service.openai_eval_runner", "[OpenAIEvalRunner] complete schedule failed: %v", err)
					}
				}()
			}
			select {
			case <-turn:
			case <-runCtx.Done():
				return
			}
			select {
			case sem <- struct{}{}:
			case <-runCtx.Done():
				return
			}
			defer func() { <-sem }()
			if r.service.accountTest != nil {
				guardCtx := context.WithValue(runCtx, openAIEvalAutomaticKey{}, true)
				if r.service.accountTest.checkOpenAIEvalAutomaticAccount(guardCtx, &Account{ID: schedule.AccountID}) != nil {
					return
				}
			}
			request := OpenAIEvalRunRequest{
				AccountID: schedule.AccountID, TestType: schedule.TestType,
				RequestedModel: schedule.RequestedModel, ReasoningEffort: schedule.ReasoningEffort,
				SampleMode: schedule.SampleMode, SampleCount: schedule.SampleCount,
			}
			if completionBased {
				latest, enabled, err := r.latestScheduledRequest(runCtx, schedule)
				if err != nil {
					logger.LegacyPrintf("service.openai_eval_runner", "[OpenAIEvalRunner] latest schedule unavailable: %v", err)
					return
				}
				if !enabled {
					return
				}
				request = latest
			}
			executeCtx := context.WithValue(runCtx, openAIEvalAdmissionTurnKey{}, signalNext)
			if _, runErr := r.service.Run(executeCtx, request, 0, "scheduled"); runErr != nil && ctx.Err() == nil {
				logger.LegacyPrintf("service.openai_eval_runner", "[OpenAIEvalRunner] scheduled run failed account=%d model=%s type=%s: %v", schedule.AccountID, schedule.RequestedModel, schedule.TestType, runErr)
			}
		}(item)
	}
	wg.Wait()
}

func (r *OpenAIEvalRunner) latestScheduledRequest(ctx context.Context, item OpenAIEvalScheduledRun) (OpenAIEvalRunRequest, bool, error) {
	request := OpenAIEvalRunRequest{AccountID: item.AccountID, TestType: item.TestType, RequestedModel: item.RequestedModel, ReasoningEffort: item.ReasoningEffort}
	cfg, err := r.repo.GetConfig(ctx)
	if err != nil || cfg == nil {
		return request, false, err
	}
	for _, route := range cfg.Accounts {
		if openAIEvalRouteKey(route.AccountID, route.RequestedModel, route.ReasoningEffort) != openAIEvalRouteKey(item.AccountID, item.RequestedModel, item.ReasoningEffort) {
			continue
		}
		var schedule OpenAIEvalSchedule
		switch item.TestType {
		case OpenAIEvalTypeCandy:
			schedule = route.CandySchedule
		case OpenAIEvalTypeFingerprint:
			schedule = route.FingerprintSchedule
		case OpenAIEvalTypeModelTrace:
			schedule = route.ModelTraceSchedule
		case OpenAIEvalTypeStateProbe:
			schedule = route.StateProbeSchedule
		}
		request.SampleMode, request.SampleCount = schedule.SampleMode, schedule.SampleCount
		return request, schedule.Enabled, nil
	}
	return request, false, nil
}
