/*
 * internal/db/metadata.go
 *
 * Helpers for reading and merging JSON metadata on prompts and video_jobs.
 */
package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	MetadataKeyVoiceScript       = "voice_script"
	MetadataKeyLocalVideoPath    = "local_video_path"
	MetadataKeyImageAnchors      = "image_anchors"
	MetadataKeyImageAnchorsLocal = "image_anchors_local"
	MetadataKeyUseImageAnchor    = "use_image_anchor"

	MetadataKeyYoutubeTitle       = "youtube_title"
	MetadataKeyYoutubeDescription = "youtube_description"
	MetadataKeyYoutubeTags        = "youtube_tags"
	MetadataKeyYoutubePrivacy     = "youtube_privacy"
	MetadataKeyYoutubePlaylistID  = "youtube_playlist_id"
)

/*
 * MetadataJSON marshals a string map to JSON for storage in TEXT columns.
 * Returns nil for empty maps.
 */
func MetadataJSON(m map[string]string) ([]byte, error) {
	if len(m) == 0 {
		return nil, nil
	}
	return json.Marshal(m)
}

/*
 * JobMetadata represents the strongly-typed schema for BAP job and prompt metadata.
 * Unmarshaling into this struct reduces dynamic allocations and simplifies data extraction.
 */
type JobMetadata struct {
	VoiceScript       string   `json:"voice_script,omitempty"`
	LocalVideoPath    string   `json:"local_video_path,omitempty"`
	ImageAnchors      []string `json:"image_anchors,omitempty"`
	ImageAnchorsLocal []string `json:"image_anchors_local,omitempty"`
	UseImageAnchor    string   `json:"use_image_anchor,omitempty"`
	YoutubeTitle      string   `json:"youtube_title,omitempty"`
	YoutubeDescription string  `json:"youtube_description,omitempty"`
	YoutubeTags       any      `json:"youtube_tags,omitempty"` // Can be a string or JSON array of strings
	YoutubePrivacy    string   `json:"youtube_privacy,omitempty"`
	YoutubePlaylistID string   `json:"youtube_playlist_id,omitempty"`
}

/*
 * ParseJobMetadata deserializes job metadata JSON safely into a JobMetadata struct.
 * Returns an empty struct if the JSON is empty or invalid.
 */
func ParseJobMetadata(metadataJSON string) *JobMetadata {
	var meta JobMetadata
	if metadataJSON != "" {
		_ = json.Unmarshal([]byte(metadataJSON), &meta)
	}
	return &meta
}

/* GetVoiceScript returns the voice script if set, otherwise the fallback value. */
func (m *JobMetadata) GetVoiceScript(fallback string) string {
	if m.VoiceScript != "" {
		return m.VoiceScript
	}
	return fallback
}

/* GetLocalVideoPath returns the local video path if set. */
func (m *JobMetadata) GetLocalVideoPath() string {
	return m.LocalVideoPath
}

/* GetImageAnchors returns the image anchors slice. */
func (m *JobMetadata) GetImageAnchors() []string {
	return m.ImageAnchors
}

/* GetImageAnchorsLocal returns the local image anchors slice. */
func (m *JobMetadata) GetImageAnchorsLocal() []string {
	return m.ImageAnchorsLocal
}

/*
 * GetUseImageAnchor returns whether to use an image anchor.
 * Translates various string formats ("true", "1", "yes") safely.
 */
func (m *JobMetadata) GetUseImageAnchor(defaultWhenUnset bool) bool {
	if m.UseImageAnchor == "" {
		return defaultWhenUnset
	}
	switch strings.ToLower(strings.TrimSpace(m.UseImageAnchor)) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return defaultWhenUnset
	}
}

/* GetYoutubeTitle returns the title to use for YouTube. If empty, returns fallback. */
func (m *JobMetadata) GetYoutubeTitle(fallback string) string {
	if m.YoutubeTitle != "" {
		return m.YoutubeTitle
	}
	return fallback
}

/* GetYoutubeDescription returns the description to use for YouTube. If empty, returns fallback. */
func (m *JobMetadata) GetYoutubeDescription(fallback string) string {
	if m.YoutubeDescription != "" {
		return m.YoutubeDescription
	}
	return fallback
}

/* GetYoutubePrivacy returns the YouTube privacy setting, normalized to lowercase. */
func (m *JobMetadata) GetYoutubePrivacy(fallback string) string {
	if m.YoutubePrivacy != "" {
		return strings.ToLower(strings.TrimSpace(m.YoutubePrivacy))
	}
	return fallback
}

/* GetYoutubePlaylistID returns the target YouTube playlist ID. */
func (m *JobMetadata) GetYoutubePlaylistID() string {
	return m.YoutubePlaylistID
}

/*
 * GetYoutubeTags parses the YouTube tags. Supports both comma-separated string
 * and JSON string array formats seamlessly.
 */
func (m *JobMetadata) GetYoutubeTags(defaultTags []string) []string {
	if m.YoutubeTags == nil {
		return defaultTags
	}
	switch v := m.YoutubeTags.(type) {
	case string:
		if v == "" {
			return defaultTags
		}
		parts := strings.Split(v, ",")
		var tags []string
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				tags = append(tags, trimmed)
			}
		}
		return tags
	case []any:
		var tags []string
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				tags = append(tags, s)
			}
		}
		if len(tags) > 0 {
			return tags
		}
	case []string:
		return v
	}
	return defaultTags
}


/*
 * MergeJobMetadata merges patch into the job's metadata JSON object.
 * Existing keys are overwritten when present in patch.
 */
func MergeJobMetadata(db *sql.DB, jobID string, patch map[string]interface{}) error {
	var existing sql.NullString
	if err := db.QueryRow(
		"SELECT metadata FROM video_jobs WHERE id = ?", jobID,
	).Scan(&existing); err != nil {
		return fmt.Errorf("fetch job metadata: %w", err)
	}

	merged := make(map[string]interface{})
	if existing.Valid && existing.String != "" {
		if err := json.Unmarshal([]byte(existing.String), &merged); err != nil {
			return fmt.Errorf("parse job metadata: %w", err)
		}
	}
	for k, v := range patch {
		merged[k] = v
	}

	metaJSON, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("marshal job metadata: %w", err)
	}

	_, err = db.Exec(
		`UPDATE video_jobs SET metadata = ?, updated_at = datetime('now') WHERE id = ?`,
		metaJSON, jobID,
	)
	return err
}


/*
 * IsProviderImageRef reports whether s is a value the AI providers can fetch (not a local path).
 */
func IsProviderImageRef(s string) bool {
	return strings.HasPrefix(s, "runway://") ||
		strings.HasPrefix(s, "luma://") ||
		strings.HasPrefix(s, "local://") ||
		strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "data:")
}

/*
 * MarkJobCompletedAfterAudio sets status to COMPLETED without changing
 * cloud_storage_url (provider URL is set earlier during polling).
 */
func MarkJobCompletedAfterAudio(db *sql.DB, jobID string) error {
	_, err := db.Exec(
		`UPDATE video_jobs
		 SET status = 'COMPLETED', updated_at = datetime('now'), completed_at = datetime('now')
		 WHERE id = ?`,
		jobID,
	)
	return err
}


