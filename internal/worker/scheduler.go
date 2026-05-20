/*
 * internal/worker/scheduler.go
 *
 * Initializes the Asynq scheduler for periodic (cron-like) tasks.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package worker

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
)

/*
 * RunScheduler starts the Asynq scheduler to enqueue periodic jobs.
 * This is a blocking call, it should be run in a goroutine.
 */
func RunScheduler(redisURL string) error {
	redisConnOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return fmt.Errorf("parse redis url: %w", err)
	}

	scheduler := asynq.NewScheduler(
		redisConnOpt,
		&asynq.SchedulerOpts{
			Location: time.Local,
			Logger:   &slogAdapter{},
		},
	)

	/* 1. Poll for processing jobs every minute */
	pollTask := asynq.NewTask(TypePollStatus, nil)
	if _, err := scheduler.Register("* * * * *", pollTask); err != nil {
		return fmt.Errorf("register poll task: %w", err)
	}

	/* 2. Start the video pipeline every 6 hours (0, 6, 12, 18) */
	startTask := asynq.NewTask(TypeStartPipeline, nil)
	if _, err := scheduler.Register("0 */6 * * *", startTask); err != nil {
		return fmt.Errorf("register start pipeline task: %w", err)
	}

	slog.Info("starting asynq scheduler")
	if err := scheduler.Run(); err != nil {
		return fmt.Errorf("run scheduler: %w", err)
	}
	return nil
}
