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

	/* TODO: Register periodic tasks here in Phase 4 */
	/* 
	 * Example: poll status every 5 minutes
	 * scheduler.Register("@every 5m", asynq.NewTask(TypePollStatus, nil))
	 */

	slog.Info("starting asynq scheduler")
	if err := scheduler.Run(); err != nil {
		return fmt.Errorf("run scheduler: %w", err)
	}
	return nil
}
