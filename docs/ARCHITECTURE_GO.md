# EasyAI Gateway — Architecture Design

> A reusable template for building a self-hosted AI API gateway with user/license management, token credit tracking, and a reverse proxy for LLM backends.

---

## 1. Overview

EasyAI Gateway is a single Go binary that serves two concurrent HTTP servers:

| Server | Default Port | Purpose |
|---|---|---|
| Admin Server | `8080` | Dashboard UI, admin API, user management |
| Ollama Proxy | `11434` | Authenticated reverse proxy to the LLM backend |

All persistent state lives in **Google Cloud Firestore**. An in-memory map mirrors Firestore at startup for low-latency reads on every request.

---

## 2. High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Internet / Clients                        │
└────────────────────┬──────────────────────┬─────────────────────┘
                     │                      │
              Admin Browser           AI Client Apps
              (Dashboard)         (VS Code ext, CLI, etc.)
                     │                      │
                     ▼                      ▼
         ┌───────────────────┐   ┌──────────────────────┐
         │   Admin Server    │   │    Ollama Proxy       │
         │   :8080 (Gin)     │   │    :11434 (Gin)       │
         │                   │   │                       │
         │  - Login UI        │   │  - Auth Middleware    │
         │  - Dashboard UI    │   │  - Credit Check       │
         │  - API Docs UI     │   │  - Model ACL          │
         │  - /api/* routes   │   │  - Token Tracking     │
         └────────┬──────────┘   └──────────┬────────────┘
                  │                         │
                  │    ┌────────────────┐   │
                  └───►│  In-Memory     │◄──┘
                       │  creditsStore  │
                       │  agentsStore   │
                       └───────┬────────┘
                               │  read/write
                               ▼
                    ┌──────────────────────┐
                    │   Google Firestore   │
                    │  collections:        │
                    │   - credits          │
                    │   - agents           │
                    └──────────────────────┘
                                           
                    ┌──────────────────────┐
                    │  Internal Ollama     │
                    │  :11435              │
                    │  (actual LLM server) │
                    └──────────────────────┘
```

---

## 3. Project Structure

```
.
├── main.go              # Entry point, router setup, all admin API handlers
├── agents.go            # Agent CRUD handlers + filtering logic
├── ollama_proxy.go      # Proxy server, auth middleware, streaming proxy handler
├── firestore_store.go   # All Firestore read/write operations
├── crypto_utils.go      # AES-GCM encrypt/decrypt helpers (for local cache files)
├── email.go             # Token generation, SMTP email sending, token store
├── go.mod / go.sum      # Go module dependencies
├── Dockerfile           # Multi-stage Docker build
├── .env.local           # Local environment config (never commit secrets)
├── templates/           # Go HTML templates (server-side rendered)
│   ├── login.html       # Admin login page
│   ├── dashboard.html   # Admin dashboard SPA
│   ├── api-docs.html    # Interactive API documentation
│   ├── email.html       # Login token email template
│   └── new-client-email.html  # New user welcome email template
├── static/
│   ├── css/dashboard.css
│   └── favicon.ico
└── docs/                # Architecture and guides
```

---

## 4. Core Data Model

### UserCredits (stored in Firestore `credits` collection)

```go
type UserCredits struct {
    LicenseID       string   // Primary key — UUID, used as the user's API key
    Email           string   // User's email address
    Balance         int      // Monthly token allocation
    CreditsTopup    int      // Additional top-up tokens
    TokensUsed      int      // Cumulative tokens consumed
    LastUpdated     int64    // Unix milliseconds
    Application     string   // Which app this license belongs to
    ModelAccessList []string // Allowed Ollama model names (empty = all)
}
```

Available tokens = `Balance + CreditsTopup - TokensUsed`

### Agent (stored in Firestore `agents` collection)

```go
type Agent struct {
    Name         string // Unique identifier
    Description  string
    Model        string // Ollama model name (e.g. "llama3:latest")
    SystemPrompt string
}
```

---

## 5. Admin Server

### 5.1 Authentication Flow

The admin uses a **magic link / token** flow — no passwords stored.

```
Browser                    Server                    Gmail SMTP
   │                          │                           │
   │  POST /request-login     │                           │
   │─────────────────────────►│                           │
   │                          │  generateToken() (64-char)│
   │                          │  storeToken(email, token) │
   │                          │  (expires in 10 minutes)  │
   │                          │──────────────────────────►│
   │                          │                    send email
   │  "Check your inbox"      │                           │
   │◄─────────────────────────│                           │
   │                          │                           │
   │  POST /login {token}     │                           │
   │─────────────────────────►│                           │
   │                          │  isValidToken() check     │
   │  302 → /dashboard        │                           │
   │◄─────────────────────────│                           │
```

- Tokens are stored in an in-memory map (`tokenMap`) with a 10-minute TTL
- Token is invalidated immediately after successful login
- `DEV_MODE=true` skips login and redirects `/` directly to `/dashboard`

### 5.2 Admin API Routes

All routes under `/api/*` require `X-API-Key: <PRIME_KEY>` header.

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/local-data` | Return all users from in-memory store |
| `GET` | `/api/credits/:licenseId` | Get a single user's credit status |
| `POST` | `/api/check-credits` | Check if a user has enough credits |
| `POST` | `/api/report-usage` | Add token usage to a user's account |
| `POST` | `/api/update-credits` | Upsert a user's credit allocation |
| `POST` | `/api/create-user` | Create a new user + send welcome email |
| `POST` | `/api/delete-user` | Delete user (requires licenseId + email match) |
| `DELETE` | `/api/delete-credits/:licenseId` | Hard delete by licenseId only |
| `PUT` | `/api/users/:licenseId/models` | Set model access list |
| `GET` | `/api/users/:licenseId/models` | Get model access list |
| `POST` | `/api/agents` | Create an agent |
| `DELETE` | `/api/agents/:agentName` | Delete an agent |

### 5.3 Dashboard UI

The dashboard is a single-page app rendered inside `dashboard.html`. It uses vanilla JavaScript with `fetch()` calls to the admin API. No frontend framework is used.

Three main panels:
- **API Usage** — read-only table of all users and their token consumption
- **LLM Providers** — editable table of model configs, saved to `localStorage`
- **User Management** — full CRUD for users, inline model access tag editor

The `PRIME_KEY` is injected into the HTML at render time via Go's template engine: `{{.PrimeKey}}`.

---

## 6. Ollama Proxy Server

Runs as a goroutine (`go StartOllamaProxy()`) on a separate port. It acts as a drop-in replacement for the Ollama API — clients point their Ollama URL here instead of the real server.

### 6.1 Middleware Chain

Every authenticated request passes through three middleware layers in order:

```
Request
   │
   ▼
OllamaProxyAuthMiddleware
   │  - Extract API key from Authorization: Bearer <key> or X-API-Key header
   │  - Look up key in creditsStore
   │  - Reject if not found (403) or no credits (403)
   │  - Set "licenseId" and "user" in Gin context
   ▼
ModelAccessMiddleware
   │  - For POST/PUT/PATCH: read JSON body, extract "model" field
   │  - Restore body for downstream handlers
   │  - Check model against user.ModelAccessList
   │  - Reject if model not allowed (403)
   │  - Set "requestModel" in context for logging
   ▼
RequestLoggingMiddleware
   │  - Log method, path, masked licenseId, model, status, duration
   ▼
Handler (TagsFilterHandler | AgentsListHandler | ProxyHandler)
```

### 6.2 Special Proxy Routes

| Route | Handler | Behaviour |
|---|---|---|
| `GET /api/tags` | `TagsFilterHandler` | Fetches model list from upstream Ollama, filters by user's `ModelAccessList` |
| `GET /api/agents` | `AgentsListHandler` | Returns agents from `agentsStore`, filtered by user's `ModelAccessList` |
| Everything else | `ProxyHandler` | Streams request/response to/from upstream Ollama |

### 6.3 Streaming & Token Tracking

`ProxyHandler` uses `bufio.Scanner` to stream NDJSON line-by-line back to the client. On each line it calls `extractTokenUsage()` looking for `done`, `eval_count`, and `prompt_eval_count` fields. When `done: true` is seen, it captures the final token counts and updates the user's `TokensUsed` in-memory and asynchronously saves to Firestore.

```
Client ──► ProxyHandler ──► Upstream Ollama
              │  stream line by line back
              │  on done=true: update credits
              ▼
         creditsStore (in-memory)
              │  async goroutine
              ▼
         Firestore
```

### 6.4 License ID Masking

All log output masks the license ID, showing only the last 4 characters:
`****-****-****-abcd`

---

## 7. Persistence Layer

### 7.1 Firestore

Two collections:

| Collection | Document ID | Contents |
|---|---|---|
| `credits` | `licenseId` | `UserCredits` struct |
| `agents` | `agent.Name` | `Agent` struct |

On startup (`init()`):
1. `initFirestore()` — creates the Firestore client using `GOOGLE_APPLICATION_CREDENTIALS`
2. `loadFromFirestore()` — loads all `credits` docs into `creditsStore` map
3. `loadAgentsFromFirestore()` — loads all `agents` docs into `agentsStore` map

All writes go to both the in-memory store and Firestore. Reads always hit the in-memory store for speed.

### 7.2 AES-GCM Encryption Utilities

`crypto_utils.go` provides `saveEncryptedCache` / `loadEncryptedCache` for writing encrypted JSON files to disk. These are available as utilities but the main data path uses Firestore directly.

---

## 8. Email System

Uses Go's standard `net/smtp` with Gmail SMTP (`smtp.gmail.com:587`).

Two email types:
- **Login token email** — sent to `ADMIN_EMAIL` on login request, uses `templates/email.html`
- **New client welcome email** — sent to the user's email on account creation, uses `templates/new-client-email.html`, includes their `licenseId` and token allocation

If `GMAIL_USER` or `GMAIL_PASS` are not set, email sending is silently skipped.

---

## 9. Configuration (Environment Variables)

| Variable | Required | Description |
|---|---|---|
| `PRIME_KEY` | Yes | Master API key for all admin API routes |
| `ADMIN_EMAIL` | Yes | Email address that receives login tokens |
| `GMAIL_USER` | Yes (for email) | Gmail address used as SMTP sender |
| `GMAIL_PASS` | Yes (for email) | Gmail app password |
| `GOOGLE_APPLICATION_CREDENTIALS` | Yes | Path to GCP service account JSON |
| `GCP_PROJECT_ID` | No | Firestore project ID (default: `easyai-gateway`) |
| `SERVER_HOST` | No | Admin server bind address (default: `0.0.0.0`) |
| `SERVER_PORT` | No | Admin server port (default: `8080`) |
| `TLS_CERT` | No | Path to TLS certificate (enables HTTPS) |
| `TLS_KEY` | No | Path to TLS private key |
| `OLLAMA_PROXY_HOST` | No | Proxy bind address (default: `0.0.0.0`) |
| `OLLAMA_PROXY_PORT` | No | Proxy port (default: `11434`) |
| `OLLAMA_INTERNAL_URL` | No | Upstream Ollama URL (default: `http://localhost:11435`) |
| `DEV_MODE` | No | `true` skips login, redirects `/` to `/dashboard` |

---

## 10. Deployment

### Docker (recommended)

Multi-stage build: Go builder → minimal Alpine runtime image.

```dockerfile
# Stage 1: Build
FROM golang:1.25-alpine AS builder
WORKDIR /app
RUN apk add --no-cache gcc musl-dev
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o main .

# Stage 2: Runtime
FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/main .
COPY static/ ./static/
COPY templates/ ./templates/
ENV SERVER_PORT=8080
EXPOSE 8080
CMD ["./main"]
```

The binary exposes two ports — make sure both are mapped if running in Docker:
```
docker run -p 8080:8080 -p 11434:11434 --env-file .env.local easyai-gateway
```

### Google Cloud Run

The app is Firebase-hosted for the static frontend (`firebase.json`) and the Go binary is deployed to Cloud Run. Cloud Run injects `PORT` via `SERVER_PORT`.

---

## 11. Security Model

| Concern | Mechanism |
|---|---|
| Admin access | Magic-link token (64-char random, 10-min TTL, single-use) |
| Admin API | `PRIME_KEY` checked on every request via `AuthMiddleware` |
| User/proxy access | `licenseId` (UUID) used as API key, checked against `creditsStore` |
| Credit enforcement | Available tokens checked before every proxied request |
| Model access control | Per-user `ModelAccessList`; empty list = all models allowed |
| Log privacy | License IDs masked to last 4 chars in all log output |
| Cache-Control | `no-cache, no-store` headers on all responses |
| Encrypted local cache | AES-GCM utilities available for any local file storage |
| TLS | Optional — set `TLS_CERT` + `TLS_KEY` to enable HTTPS |

---

## 12. Key Dependencies

| Package | Purpose |
|---|---|
| `github.com/gin-gonic/gin` | HTTP router and middleware for both servers |
| `cloud.google.com/go/firestore` | Persistent storage |
| `github.com/google/uuid` | License ID generation |
| `github.com/joho/godotenv` | Load `.env.local` at startup |
| `net/smtp` (stdlib) | Email delivery |
| `crypto/aes`, `crypto/cipher` (stdlib) | AES-GCM encryption |
| `bufio.Scanner` (stdlib) | NDJSON streaming in proxy |

---

## 13. Adapting This as a Template

To build a new application using this architecture:

1. **Replace the LLM backend** — swap Ollama for any HTTP API by changing `OLLAMA_INTERNAL_URL` and updating `ProxyHandler` to match the target API's response format for token counting.

2. **Replace the resource being gated** — `UserCredits` tracks tokens, but the same pattern works for API calls, file exports, seats, or any countable resource. Rename fields accordingly.

3. **Replace Firestore** — the persistence layer is isolated in `firestore_store.go`. Swap it for Postgres, Redis, or any other store by reimplementing the 8 functions in that file.

4. **Extend the data model** — add fields to `UserCredits` for your domain (e.g. `Plan`, `ExpiresAt`, `Features []string`).

5. **Add more proxy routes** — follow the `TagsFilterHandler` pattern: authenticate via middleware, call upstream, transform/filter response, return JSON.

6. **Multi-tenant agents** — the `Agent` + `filterAgents` pattern is reusable for any named configuration object that should be scoped per user.

7. **Keep the email flow** — the magic-link login is production-ready as-is. Just update `templates/email.html` with your branding.
