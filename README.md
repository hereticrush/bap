# BAP (Build And Post) - Automated Video Production Pipeline

BAP is a fully automated, headless video production engine. It takes simple "seed" ideas, expands them using an LLM, generates breathtaking image anchors, turns those anchors into AI video, layers human-like voiceovers, and ultimately publishes the finished video directly to YouTube—all automatically on a schedule.

This guide will walk you through creating and publishing your first video.

## Step 1: Configuration & Credentials

Before running the pipeline, you need to provide your API keys and YouTube authorization.

1. **API Keys**: Copy `.env.example` to `.env` and fill in your keys for the various AI services:
   - `GEMINI_API_KEY`: Used to transform simple ideas into detailed, highly-descriptive video prompts.
   - `RUNWAY_API_KEY`: Used to generate the final video from the prompt and image anchor.
   - `ELEVENLABS_API_KEY`: Used to generate realistic text-to-speech voiceovers.
   - `ELEVENLABS_VOICE_ID`: ElevenLabs voice ID for TTS (optional; a default is used if unset).

2. **YouTube Credentials**: 
   - You need a Google Cloud project with the YouTube Data API v3 enabled.
   - Download the OAuth 2.0 Client ID JSON and save it as `credentials/youtube/client_secret.json`.
   - On the very first run, the system will prompt you in the terminal to authorize the app and generate a `token.json`.

## Step 2: Write Your Seeds

The system operates on "seeds"—simple ideas that the LLM expands into rich prompts.

1. Open `seeds.example.json`.
2. Add your ideas into the `seeds` array. For example:
```json
{
  "system_prompt": "You are an expert cinematic video director...",
  "target_provider": "RUNWAY",
  "seeds": [
    {
      "seed_text": "A cyber-samurai standing in the neon rain.",
      "metadata": {
        "voice_script": "The neon lights flickered as the samurai drew his blade."
      }
    }
  ]
}
```

## Step 3: Enrich & Load the Prompts

Now, we use the `bap` CLI to process these seeds. The LLM (Gemini) will take your short seed text and expand it into a dense, highly descriptive prompt optimized for AI video generators. 

Run the prompt builder through Docker:
```bash
docker exec -it bap-app-1 /app/bap build-prompts /app/seeds.example.json
```
*(This truncates the old prompt queue and loads the newly enriched prompts into the SQLite database as `UNUSED`).*

## Step 4: Start the Engine

Bring up the entire system using Docker Compose:
```bash
docker-compose up -d --build
```

### What happens now?

The system is now fully autonomous! Every **6 hours**, the built-in Asynq scheduler will wake up and trigger the production pipeline:

1. **Pipeline start** (`video:start_pipeline`, every 6 hours): Claims one `UNUSED` prompt and creates a job.
2. **Image anchors (optional)**: When enabled for that seed (`use_image_anchor` in metadata, or `ENABLE_IMAGE_ANCHORS` when omitted), Pollinations.ai generates a local PNG, then Runway **ephemeral upload** stores a `runway://` URI in `metadata.image_anchors` (local path kept in `metadata.image_anchors_local`).
3. **Video Generation**: Runway receives the enriched prompt and, when anchors are enabled, the `runway://` image reference—not a VPS file path.
4. **Polling & Downloading**: The system checks Runway's status periodically. Once complete, it downloads the raw `.mp4` to `data/videos/`.
5. **Voiceover & Mixing**: ElevenLabs uses per-seed `voice_script` when present (otherwise the enriched prompt), then `ffmpeg` merges audio with video.
6. **Publishing**: The finished video uploads to YouTube as Private; description uses the enriched prompt.

Per-seed metadata keys (string values): `voice_script`, `use_image_anchor` (`"true"` / `"false"`). Ephemeral upload requires Runway API credits.

## Observability

You can monitor the live queue and see what the pipeline is currently working on by checking the built-in Health/Observability API:
```bash
curl http://localhost:8081/api/jobs
```
This returns a JSON array of recent jobs, showing their current status (`PENDING`, `IMAGE_READY`, `PROCESSING`, `COMPLETED`, `VIDEO_READY`, `PUBLISHED`, or `FAILED`), metadata (`voice_script`, `image_anchors`, `image_anchors_local`, `local_video_path`), and any error logs. The `cloud_storage_url` field holds the provider download URL only.
