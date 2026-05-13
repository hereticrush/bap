/*
 * internal/worker/server.go
 *
 * Initializes the Asynq server and registers task handlers.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package worker

import (
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/hereticrush/bap/internal/adapter/video"
	"hereticrush/bap/internal/publisher"
	"github.com/hibiken/asynq"
)

/*
 * RunServer starts the Asynq worker server to consume background jobs.
 * This is a blocking call, it should be run in a goroutine.
 */
func RunServer(redisURL string, db *sql.DB, provider video.AIVideoProvider, pub publisher.Publisher, videoOutputDir string) error {
	redisConnOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return fmt.Errorf("parse redis url: %w", err)
	}

	srv := asynq.NewServer(
		redisConnOpt,
		asynq.Config{
			Concurrency: 10,
			Queues: map[string]int{
				"critical": 6,
				"default":  3,
				"low":      1,
			},
			Logger: &slogAdapter{},
		},
	)

	processor := &VideoProcessor{
		DB:             db,
		Provider:       provider,
		Publisher:      pub,
		Client:         asynq.NewClient(redisConnOpt),
		VideoOutputDir: videoOutputDir,
	}
	defer processor.Client.Close()

	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeGenerateVideo, processor.HandleGenerateVideoTask)
	mux.HandleFunc(TypePollStatus, processor.HandlePollStatusTask)
	mux.HandleFunc(TypeDownloadVideo, processor.HandleDownloadVideoTask)
	mux.HandleFunc(TypePublishVideo, processor.HandlePublishVideoTask)

	slog.Info("starting asynq worker server", "redis_url", redisURL)
	if err := srv.Run(mux); err != nil {
		return fmt.Errorf("run server: %w", err)
	}
	return nil
}

/* slogAdapter wraps slog to implement asynq.Logger */
type slogAdapter struct{}

func (a *slogAdapter) Debug(args ...interface{}) { slog.Debug("asynq", args...) }
func (a *slogAdapter) Info(args ...interface{})  { slog.Info("asynq", args...) }
func (a *slogAdapter) Warn(args ...interface{})  { slog.Warn("asynq", args...) }
func (a *slogAdapter) Error(args ...interface{}) { slog.Error("asynq", args...) }
func (a *slogAdapter) Fatal(args ...interface{}) { slog.Error("asynq fatal", args...) }
