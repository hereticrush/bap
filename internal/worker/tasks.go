/*
 * internal/worker/tasks.go
 *
 * Defines Asynq task types and payload structures for the video pipeline.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package worker

/* Task type constants for the Asynq message broker. */
const (
	TypeGenerateVideo = "video:generate"
	TypePollStatus    = "video:poll"
	TypeDownloadVideo = "video:download"
	TypeAddAudio      = "video:add_audio"
	TypePublishVideo  = "video:publish"
)

/* Add any struct definitions for task payloads here if needed in the future. */
