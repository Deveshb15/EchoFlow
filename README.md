<h1 align="center">EchoFlow</h1>

<p align="center">
  Self-hosted dictation/transcription API inspired by <a href="https://github.com/zachlatta/freeflow">FreeFlow</a>, built for the same <a href="https://wisprflow.ai">Wispr Flow</a> / <a href="https://superwhisper.com">Superwhisper</a> / <a href="https://monologue.to">Monologue</a> style workflow.
</p>

<p align="center">
  Bring your own Groq token, send audio, get cleaned text back.
</p>

---

EchoFlow is a small Go API that replicates the remote parts of a dictation pipeline:

- audio transcription (OpenAI-compatible `/audio/transcriptions` upstream)
- text post-processing (OpenAI-compatible `/chat/completions` upstream)
- combined pipeline endpoint with fallback to raw transcript when post-processing fails

It is designed for single-user self-hosted use first and does not persist user data.

## How It Works

1. Run EchoFlow locally (or on your own server)
2. Get a Groq Cloud API key from [groq.com](https://groq.com/)
3. Send requests to EchoFlow with `Authorization: Bearer <your_groq_cloud_token>`
4. EchoFlow forwards requests to Groq and returns normalized transcription / post-processing responses

## Endpoints

- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
- `POST /v1/transcriptions`
- `POST /v1/post-process`
- `POST /v1/pipeline/process`

Base URL (default): `http://localhost:8080`

## BYOT (Bring Your Own Token)

EchoFlow uses BYOT by default.

- Send your Groq Cloud token on API requests:
  - `Authorization: Bearer <your_groq_cloud_token>`
- `UPSTREAM_API_KEY` is optional and acts as a server-side fallback token when no request token is provided.
- In pure BYOT mode (no `UPSTREAM_API_KEY`), `GET /readyz` skips the upstream probe unless a token is present.

## Run (Local)

```bash
cp .env.example .env
# Optional: set UPSTREAM_API_KEY in .env if you want a server-side fallback token
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

## Example: Combined Pipeline Response

```json
{
  "raw_transcript": "um hey can you email alise about the deploy",
  "final_transcript": "Hey, can you email Alice about the deploy?",
  "post_processing_status": "Post-processing succeeded",
  "post_processing_usage": {
    "prompt_tokens": 128,
    "completion_tokens": 16,
    "total_tokens": 144
  },
  "timings_ms": {
    "transcription": 312,
    "post_processing": 208,
    "total": 520
  }
}
```

Note: responses no longer include debug prompt text (`prompt` / `post_processing_prompt`). EchoFlow returns token usage metadata instead when the upstream provider includes `usage`.

## Docker

```bash
docker build -t echoflow .
docker run --rm -p 8080:8080 echoflow
```

Optional Docker fallback token:

```bash
docker run --rm -p 8080:8080 \
  -e UPSTREAM_API_KEY=your_groq_key \
  echoflow
```

## FAQ

**Why BYOT (Bring Your Own Token)?**

It keeps EchoFlow simple and avoids turning this project into a hosted SaaS with accounts/billing. You use your own Groq key directly.

**Does EchoFlow store my audio or transcripts?**

No persistence is built in by default. EchoFlow processes requests and returns results. The only external calls are to your configured OpenAI-compatible upstream (Groq by default).

**Why Groq instead of local models?**

Same tradeoff as FreeFlow: speed and UX. The pipeline feels much better when transcription + post-processing complete quickly. Local-only setups are possible, but they typically increase latency and complexity.

**Can I use something other than Groq?**

Yes. EchoFlow uses an OpenAI-compatible API interface. Set `UPSTREAM_BASE_URL` and send a compatible token in `Authorization` (or set `UPSTREAM_API_KEY` as a fallback).

**What does `GET /readyz` do in BYOT mode?**

If a token is available (request `Authorization` header or `UPSTREAM_API_KEY`), EchoFlow checks the upstream `/models` endpoint. If no token is available, it returns OK without probing upstream.
