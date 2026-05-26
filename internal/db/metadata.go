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
 * TTSText returns the text to send to TTS: voice_script from metadata when set,
 * otherwise the enriched prompt snapshot.
 */
func TTSText(metadataJSON string, promptSnapshot string) string {
	if metadataJSON != "" {
		var meta map[string]string
		if err := json.Unmarshal([]byte(metadataJSON), &meta); err == nil {
			if script := meta[MetadataKeyVoiceScript]; script != "" {
				return script
			}
		}
	}
	return promptSnapshot
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
 * UseImageAnchor returns whether this job should generate and upload an image
 * anchor. Per-seed metadata use_image_anchor ("true"/"false") overrides
 * defaultWhenUnset when present.
 */
func UseImageAnchor(metadataJSON string, defaultWhenUnset bool) bool {
	if metadataJSON == "" {
		return defaultWhenUnset
	}
	var meta map[string]string
	if err := json.Unmarshal([]byte(metadataJSON), &meta); err != nil {
		return defaultWhenUnset
	}
	v, ok := meta[MetadataKeyUseImageAnchor]
	if !ok {
		return defaultWhenUnset
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return defaultWhenUnset
	}
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

/*
 * GetYoutubeTitle returns the title to use for YouTube. If set in metadata,
 * it returns that; otherwise it falls back to the default value.
 */
func GetYoutubeTitle(metadataJSON string, fallback string) string {
	if metadataJSON != "" {
		var meta map[string]string
		if err := json.Unmarshal([]byte(metadataJSON), &meta); err == nil {
			if title := meta[MetadataKeyYoutubeTitle]; title != "" {
				return title
			}
		}
	}
	return fallback
}

/*
 * GetYoutubeDescription returns the description to use for YouTube.
 * If set in metadata, it returns that; otherwise it falls back to the default value.
 */
func GetYoutubeDescription(metadataJSON string, fallback string) string {
	if metadataJSON != "" {
		var meta map[string]string
		if err := json.Unmarshal([]byte(metadataJSON), &meta); err == nil {
			if desc := meta[MetadataKeyYoutubeDescription]; desc != "" {
				return desc
			}
		}
	}
	return fallback
}

/*
 * GetYoutubePrivacy returns the privacy to use for YouTube.
 * If set in metadata, it returns that; otherwise it falls back to the default value.
 */
func GetYoutubePrivacy(metadataJSON string, fallback string) string {
	if metadataJSON != "" {
		var meta map[string]string
		if err := json.Unmarshal([]byte(metadataJSON), &meta); err == nil {
			if privacy := meta[MetadataKeyYoutubePrivacy]; privacy != "" {
				return strings.ToLower(strings.TrimSpace(privacy))
			}
		}
	}
	return fallback
}

/*
 * GetYoutubePlaylistID returns the playlist ID to use for YouTube.
 * If set in metadata, it returns that; otherwise it returns empty.
 */
func GetYoutubePlaylistID(metadataJSON string) string {
	if metadataJSON != "" {
		var meta map[string]string
		if err := json.Unmarshal([]byte(metadataJSON), &meta); err == nil {
			return meta[MetadataKeyYoutubePlaylistID]
		}
	}
	return ""
}

/*
 * GetYoutubeTags returns the tags to use for YouTube. If set in metadata
 * as a JSON array or a comma-separated string, it parses them;
 * otherwise it returns the default tags.
 */
func GetYoutubeTags(metadataJSON string, defaultTags []string) []string {
	if metadataJSON == "" {
		return defaultTags
	}
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(metadataJSON), &meta); err == nil {
		if val, ok := meta[MetadataKeyYoutubeTags]; ok {
			switch v := val.(type) {
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
			case []interface{}:
				var tags []string
				for _, item := range v {
					if s, ok := item.(string); ok && s != "" {
						tags = append(tags, s)
					}
				}
				if len(tags) > 0 {
					return tags
				}
			}
		}
	}
	return defaultTags
}
