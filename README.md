# EchoFlow

Inspired by [zachlatta/freeflow](https://github.com/zachlatta/freeflow).

EchoFlow is a small Go API that replicates the remote parts of the [WisprFlow](https://wisprflow.ai/), [Monologue](http://monologue.to/), and [Superwhisper](https://superwhisper.com) style dictation pipeline:

- audio transcription (OpenAI-compatible `/audio/transcriptions` upstream)
- text post-processing (OpenAI-compatible `/chat/completions` upstream)
- combined pipeline endpoint with fallback to raw transcript when post-processing fails

It is designed for single-user self-hosted use first and does not persist user data.

## Endpoints

- `GET /healthz`
- `GET /readyz`
- `POST /v1/transcriptions`
- `POST /v1/post-process`
- `POST /v1/pipeline/process`

## Run

```bash
cp .env.example .env
export $(grep -v '^#' .env | xargs)
go run ./cmd/echoflow-api
```

## Example: Transcribe Audio

```bash
curl -X POST http://localhost:8080/v1/transcriptions \
  -F model=whisper-large-v3 \
  -F file=@sample.wav
```

Responses no longer include debug prompt text (`prompt`/`post_processing_prompt`). Instead, post-processing responses include token usage metadata when the upstream provider returns `usage`.

## Example: Post-Process Transcript

```bash
curl -X POST http://localhost:8080/v1/post-process \
  -H 'Content-Type: application/json' \
  -d '{
    "transcript": "um hey can you email alise about the deploy",
    "context_summary": "User is replying to an email to Alice about a deployment.",
    "custom_vocabulary": "Alice, deploy"
  }'
```

## Example: Combined Pipeline

```bash
curl -X POST http://localhost:8080/v1/pipeline/process \
  -F file=@sample.wav \
  -F context_summary='User is replying in email to Alice.' \
  -F custom_vocabulary='Alice, staging, prod'
```
