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
- `GET /metrics`
- `POST /v1/transcriptions`
- `POST /v1/post-process`
- `POST /v1/pipeline/process`

Base URL (default): `http://localhost:8080`

## BYOT (Bring Your Own Token)

EchoFlow supports BYOT by default.

- Send your Groq Cloud API token in the `Authorization` header on API requests:
  - `Authorization: Bearer <your_groq_cloud_token>`
- `UPSTREAM_API_KEY` is optional and acts as a server-side fallback when no request token is provided.
- In BYOT mode, `GET /readyz` skips the upstream probe unless a token is available (request `Authorization` header or `UPSTREAM_API_KEY`).

## Run (Local)

```bash
cp .env.example .env
# Optional: set UPSTREAM_API_KEY in .env to use a server-side fallback token.
set -a
source .env
set +a

go run ./cmd/echoflow-api
```

Or with Make:

```bash
make run
```

## Dev Commands

```bash
make fmt
make test
make vet
make tidy
```

Responses no longer include debug prompt text (`prompt`/`post_processing_prompt`). Instead, post-processing responses include token usage metadata when the upstream provider returns `usage`.

## Example: Transcribe Audio

```bash
curl -X POST http://localhost:8080/v1/transcriptions \
  -H "Authorization: Bearer $GROQ_API_KEY" \
  -F model=whisper-large-v3 \
  -F file=@sample.wav
```

## Example: Post-Process Transcript

```bash
curl -X POST http://localhost:8080/v1/post-process \
  -H "Authorization: Bearer $GROQ_API_KEY" \
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
  -H "Authorization: Bearer $GROQ_API_KEY" \
  -F file=@sample.wav \
  -F context_summary='User is replying in email to Alice.' \
  -F custom_vocabulary='Alice, staging, prod'
```

## Docker

```bash
docker build -t echoflow .
docker run --rm -p 8080:8080 \
  echoflow
```

Optional Docker fallback token:

```bash
docker run --rm -p 8080:8080 \
  -e UPSTREAM_API_KEY=your_groq_key \
  echoflow
```
