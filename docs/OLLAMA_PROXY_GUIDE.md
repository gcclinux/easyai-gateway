# Ollama Proxy Guide

## Architecture Overview

```
┌──────────────────┐       ┌─────────────────────────┐       ┌──────────────────┐
│   Client          │       │   EasyAI Gateway         │       │   Ollama Server   │
│  (curl, Open     │       │                           │       │                   │
│   WebUI, etc.)   │       │  Admin API  :5555         │       │  :11435 (internal)│
│                  │──────▶│  Ollama Proxy :11434      │──────▶│                   │
│                  │       │                           │       │                   │
└──────────────────┘       └─────────────────────────┘       └──────────────────┘
```

The gateway runs two servers concurrently:

| Server | Default Port | Purpose |
|--------|-------------|---------|
| Admin API | `SERVER_PORT` (5555) | User management, credits, model access, agents CRUD |
| Ollama Proxy | `OLLAMA_PROXY_PORT` (11434) | Authenticated reverse proxy to Ollama |

Ollama itself listens on an internal port (11435) that is not exposed externally. All client traffic goes through the proxy on 11434.

---

## Environment Variables

Add these to your `.env.local`:

```env
# Admin server
SERVER_HOST=0.0.0.0
SERVER_PORT=5555

# Ollama proxy
OLLAMA_PROXY_HOST=0.0.0.0
OLLAMA_PROXY_PORT=11434
OLLAMA_INTERNAL_URL=http://localhost:11435
```

If not set, the proxy defaults to `0.0.0.0:11434` forwarding to `http://localhost:11435`.

---

## Startup

Run the gateway:

```bash
go run .
```

You should see output like this:

```
[GIN-debug] GET    /                         --> main.HomeHandler (4 handlers)
[GIN-debug] POST   /request-login            --> main.RequestLoginHandler (4 handlers)
[GIN-debug] POST   /login                    --> main.LoginHandler (4 handlers)
[GIN-debug] GET    /dashboard                --> main.DashboardHandler (4 handlers)
[GIN-debug] GET    /api-docs                 --> main.ApiDocsHandler (4 handlers)
[GIN-debug] GET    /api/local-data           --> main.LocalDataHandler (5 handlers)
[GIN-debug] GET    /api/credits/:licenseId   --> main.GetCreditsHandler (5 handlers)
[GIN-debug] POST   /api/check-credits        --> main.CheckCreditsHandler (5 handlers)
[GIN-debug] POST   /api/report-usage         --> main.ReportUsageHandler (5 handlers)
[GIN-debug] POST   /api/update-credits       --> main.UpdateCreditsHandler (5 handlers)
[GIN-debug] DELETE /api/delete-credits/:licenseId --> main.DeleteCreditsHandler (5 handlers)
[GIN-debug] POST   /api/create-user          --> main.CreateUserHandler (5 handlers)
[GIN-debug] POST   /api/delete-user          --> main.DeleteUserHandler (5 handlers)
[GIN-debug] PUT    /api/users/:licenseId/models --> main.UpdateModelAccessHandler (5 handlers)
[GIN-debug] GET    /api/users/:licenseId/models --> main.GetModelAccessHandler (5 handlers)
[GIN-debug] POST   /api/agents               --> main.CreateAgentHandler (5 handlers)
[GIN-debug] DELETE /api/agents/:agentName    --> main.DeleteAgentHandler (5 handlers)
=====================================================
Easy AI API Gateway
-----------------------------------------------------
Local Access:   http://localhost:5555
Network Access: http://your-ip-address:5555
=====================================================
[GIN-debug] Listening and serving HTTP on 0.0.0.0:5555

[GIN-debug] GET    /health                   --> main.StartOllamaProxy.func1 (3 handlers)
[GIN-debug] GET    /api/tags                 --> main.StartOllamaProxy.TagsFilterHandler.func6 (6 handlers)
[GIN-debug] GET    /api/agents               --> main.StartOllamaProxy.AgentsListHandler.func7 (6 handlers)
=====================================================
Ollama Proxy
-----------------------------------------------------
Listening on: 0.0.0.0:11434
Upstream:     http://localhost:11435
=====================================================
```

Two blocks of routes appear:
- The first block (5 handlers) is the **Admin API** on port 5555, protected by `PRIME_KEY`.
- The second block (6 handlers) is the **Ollama Proxy** on port 11434, protected by user License IDs.

---

## Request Flow

```
Client                        Proxy (:11434)                  Ollama (:11435)
  │                               │                               │
  │  POST /api/chat               │                               │
  │  Authorization: Bearer <key>  │                               │
  │──────────────────────────────▶│                               │
  │                               │  1. Extract API key           │
  │                               │  2. Lookup license in store   │
  │                               │  3. Check credits > 0         │
  │                               │  4. Check model access        │
  │                               │──────────────────────────────▶│
  │                               │                               │
  │                               │◀─── Stream NDJSON chunks ─────│
  │◀─── Stream chunks ───────────│                               │
  │                               │  5. Extract token usage       │
  │                               │  6. Update user credits       │
  │                               │                               │
```

---

## Authentication

### Admin API (port 5555)

All admin endpoints require the `PRIME_KEY` in the `X-API-Key` header:

```bash
curl -H "X-API-Key: YOUR_PRIME_KEY" http://localhost:5555/api/local-data
```

### Ollama Proxy (port 11434)

Proxy endpoints require a user's License ID via either header:

```bash
# Option 1: Bearer token
curl -H "Authorization: Bearer USER_LICENSE_ID" http://localhost:11434/api/tags

# Option 2: X-API-Key header
curl -H "X-API-Key: USER_LICENSE_ID" http://localhost:11434/api/tags
```

---

## Admin API Endpoints (port 5555)

All endpoints below require `X-API-Key: PRIME_KEY`.

### Model Access Management

#### Set a user's allowed models

```bash
curl -X PUT http://localhost:5555/api/users/USER_LICENSE_ID/models \
     -H "Content-Type: application/json" \
     -H "X-API-Key: YOUR_PRIME_KEY" \
     -d '{"models": ["llama3:latest", "mistral:latest"]}'
```

An empty list grants access to all models:

```bash
curl -X PUT http://localhost:5555/api/users/USER_LICENSE_ID/models \
     -H "Content-Type: application/json" \
     -H "X-API-Key: YOUR_PRIME_KEY" \
     -d '{"models": []}'
```

#### Get a user's allowed models

```bash
curl -H "X-API-Key: YOUR_PRIME_KEY" \
     http://localhost:5555/api/users/USER_LICENSE_ID/models
```

Response:
```json
{
  "models": ["llama3:latest", "mistral:latest"]
}
```

### Agent Management

#### Create an agent

```bash
curl -X POST http://localhost:5555/api/agents \
     -H "Content-Type: application/json" \
     -H "X-API-Key: YOUR_PRIME_KEY" \
     -d '{
       "name": "code-assistant",
       "description": "Helps with coding tasks",
       "model": "llama3:latest",
       "systemPrompt": "You are a helpful coding assistant."
     }'
```

#### Delete an agent

```bash
curl -X DELETE http://localhost:5555/api/agents/code-assistant \
     -H "X-API-Key: YOUR_PRIME_KEY"
```

---

## Ollama Proxy Endpoints (port 11434)

All endpoints below require a valid user License ID.

### Health check (no auth required)

```bash
curl http://localhost:11434/health
```

Response: `{"status":"ok"}`

### List available models

Returns models filtered by the user's access list.

```bash
curl -H "Authorization: Bearer USER_LICENSE_ID" \
     http://localhost:11434/api/tags
```

### List available agents

Returns agents filtered by the user's model access list.

```bash
curl -H "Authorization: Bearer USER_LICENSE_ID" \
     http://localhost:11434/api/agents
```

### Chat completion (streaming)

```bash
curl -H "Authorization: Bearer USER_LICENSE_ID" \
     -H "Content-Type: application/json" \
     http://localhost:11434/api/chat \
     -d '{
       "model": "llama3:latest",
       "messages": [{"role": "user", "content": "Hello!"}]
     }'
```

### Generate completion

```bash
curl -H "Authorization: Bearer USER_LICENSE_ID" \
     -H "Content-Type: application/json" \
     http://localhost:11434/api/generate \
     -d '{
       "model": "llama3:latest",
       "prompt": "Why is the sky blue?"
     }'
```

### Any other Ollama endpoint

All standard Ollama API paths are proxied transparently. Just point your Ollama-compatible client to `http://localhost:11434` and include your License ID as a Bearer token.

---

## Error Responses

All errors return JSON in the format `{"error": "message"}`.

| Status | Error | Meaning |
|--------|-------|---------|
| 401 | `unauthorized: API key required` | No License ID provided |
| 403 | `access denied: invalid API key` | License ID not found |
| 403 | `insufficient credits` | User has no remaining credits |
| 403 | `access denied: model not authorized` | Model not in user's access list |
| 404 | `user not found` | License ID not found (admin endpoints) |
| 404 | `agent not found` | Agent name not found (delete) |
| 502 | `upstream server unavailable` | Ollama server is not reachable |

---

## Usage Tracking

The proxy automatically tracks token usage from Ollama responses. When Ollama returns a final streaming chunk with `done: true`, the proxy extracts `eval_count` and `prompt_eval_count` and adds them to the user's `usedToken` balance.

Available credits are calculated as:

```
available = monthlyToken + topUpToken - usedToken
```

A request is allowed when `available > 0`.

---

## Configuring Ollama to Listen on the Internal Port

By default Ollama listens on `11434`. Since the proxy takes over that port, reconfigure Ollama to use `11435`:

**Linux/macOS** (systemd or env):
```bash
export OLLAMA_HOST=127.0.0.1:11435
ollama serve
```

**Windows** (environment variable):
```powershell
$env:OLLAMA_HOST = "127.0.0.1:11435"
ollama serve
```

This ensures Ollama only accepts connections from localhost on the internal port, while the proxy handles all external traffic on 11434.

---

## Using with Open WebUI

Point Open WebUI to the proxy instead of Ollama directly:

```
OLLAMA_BASE_URL=http://localhost:11434
```

In Open WebUI settings, set the API key to the user's License ID. The proxy handles auth, model filtering, and usage tracking transparently.

---

## Quick Setup Checklist

1. Configure Ollama to listen on `127.0.0.1:11435`
2. Set your `.env.local` with `PRIME_KEY`, `SERVER_PORT`, and optionally `OLLAMA_PROXY_PORT` / `OLLAMA_INTERNAL_URL`
3. Run `go run .`
4. Create a user via the admin API (`POST /api/create-user`)
5. Optionally restrict their model access (`PUT /api/users/:licenseId/models`)
6. Give the user their License ID — they use it as a Bearer token against port 11434
7. Monitor usage on the dashboard at `http://localhost:5555/dashboard`
