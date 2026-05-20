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

1. **Image Anchor Generation**: The system claims one `UNUSED` prompt from the database and calls Pollinations.ai to generate a stunning reference image.
2. **Video Generation**: The text prompt and the generated image anchor are sent to Runway Gen-3 to generate the video.
3. **Polling & Downloading**: The system checks Runway's status periodically. Once complete, it downloads the raw `.mp4` to `data/videos/`.
4. **Voiceover & Mixing**: The system calls ElevenLabs using the per-seed `voice_script` in metadata when present (otherwise it falls back to the enriched prompt text), then uses `ffmpeg` to merge the audio track with the video.
5. **Publishing**: The final, complete video is securely uploaded to your YouTube channel as a Private video, using the generated prompt as the video description.

## Observability

You can monitor the live queue and see what the pipeline is currently working on by checking the built-in Health/Observability API:
```bash
curl http://localhost:8081/api/jobs
```
This returns a JSON array of recent jobs, showing their current status (`PENDING`, `IMAGE_READY`, `PROCESSING`, `COMPLETED`, `VIDEO_READY`, `PUBLISHED`, or `FAILED`), metadata (including `voice_script` and `local_video_path`), and any error logs. The `cloud_storage_url` field holds the provider download URL only.
