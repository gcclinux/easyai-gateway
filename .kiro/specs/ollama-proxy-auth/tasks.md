# Tasks: Ollama Proxy Auth

## Task 1: Update Data Models and Shared State

- [x] 1.1 Add `ModelAccessList []string` field to `UserCredits` struct in `main.go` with JSON tag `modelAccessList,omitempty`
- [x] 1.2 Update `UserCredits.UnmarshalJSON` to handle the new `modelAccessList` field
- [x] 1.3 Add `sync.RWMutex` for `creditsStore` concurrent access and wrap existing reads/writes
- [x] 1.4 Create `Agent` struct with `Name`, `Description`, `Model`, `SystemPrompt` fields
- [x] 1.5 Create `agentsStore` in-memory map and Firestore load/save/delete functions in `firestore_store.go`

## Task 2: Proxy Configuration and Server Startup

- [x] 2.1 Create `ollama_proxy.go` with `resolveProxyConfig()` function reading `OLLAMA_PROXY_HOST`, `OLLAMA_PROXY_PORT`, `OLLAMA_INTERNAL_URL` env vars with defaults
- [x] 2.2 Implement `StartOllamaProxy()` function that creates a new Gin engine and starts listening on the configured address
- [x] 2.3 Update `main.go` to call `go StartOllamaProxy()` before the admin server starts, and load agents from Firestore in `init()`

## Task 3: Authentication Middleware

- [x] 3.1 Implement `extractAPIKey()` function that extracts API key from `Authorization: Bearer` header with `X-API-Key` fallback
- [x] 3.2 Implement `checkCredits()` function that returns whether a user has available credits (`monthlyToken + topUpToken - usedToken > 0`)
- [x] 3.3 Implement `OllamaProxyAuthMiddleware()` Gin middleware that uses `extractAPIKey` and `checkCredits`, sets user in context, returns 401/403 on failure

## Task 4: Model Access Control

- [x] 4.1 Implement `checkModelAccess(model string, accessList []string) bool` function
- [x] 4.2 Implement `ModelAccessMiddleware()` Gin middleware that reads JSON body, extracts `model` field, checks access, restores body for downstream
- [x] 4.3 Implement `filterModels()` function that filters an Ollama model list by a user's `ModelAccessList`
- [x] 4.4 Implement `TagsFilterHandler()` for `GET /api/tags` that forwards to Ollama and filters the response

## Task 5: Proxy Handler and Streaming

- [x] 5.1 Implement `ProxyHandler()` that builds upstream URL, copies request (method, path, query, body, non-auth headers), forwards to Ollama
- [x] 5.2 Implement streaming response passthrough using `io.Copy` with `http.Flusher` for NDJSON streaming support
- [x] 5.3 Implement `extractTokenUsage()` function that parses JSON for `done`, `eval_count`, `prompt_eval_count` fields
- [x] 5.4 Integrate token extraction into streaming: inspect each NDJSON line, capture usage from final chunk (`done: true`), update user credits
- [x] 5.5 Handle upstream connection failure with HTTP 502 response

## Task 6: Admin API for Model Access Management

- [x] 6.1 Add `PUT /api/users/:licenseId/models` handler to update a user's `ModelAccessList` in creditsStore and Firestore
- [x] 6.2 Add `GET /api/users/:licenseId/models` handler to return a user's current `ModelAccessList`
- [x] 6.3 Register both endpoints in the existing admin API group (PRIME_KEY protected)

## Task 7: Agents Feature

- [x] 7.1 Create `agents.go` with `CreateAgentHandler` (POST /api/agents) and `DeleteAgentHandler` (DELETE /api/agents/:agentName)
- [x] 7.2 Implement `filterAgents()` function that filters agents by user's `ModelAccessList`
- [x] 7.3 Implement `AgentsListHandler` (GET /api/agents) on the proxy that returns filtered agents for authenticated users
- [x] 7.4 Register agent admin routes in the existing admin API group and agent list route on the proxy

## Task 8: Logging and Observability

- [x] 8.1 Implement `maskLicenseID()` function that masks all but last 4 characters of a license ID
- [x] 8.2 Add request logging middleware to the proxy: log method, path, masked license ID, model, and on completion log status code and duration
- [x] 8.3 Add auth failure logging with failure reason and client IP

## Task 9: Property-Based Tests

- [x] 9.1 Add `gopter` dependency and create `ollama_proxy_test.go` with property tests for `extractAPIKey`, `checkCredits`, `checkModelAccess`, `filterModels`, `maskLicenseID`, `resolveProxyConfig`
- [x] 9.2 Create property tests for `extractTokenUsage` (Property 11) and credit update logic (Property 12)
- [x] 9.3 Create `agents_test.go` with property tests for `filterAgents` (Property 13)

## Task 10: Unit and Integration Tests

- [x] 10.1 Write unit tests for auth middleware (401/403/pass-through scenarios)
- [x] 10.2 Write unit tests for model access middleware and tags filtering
- [x] 10.3 Write unit tests for proxy handler (502 on upstream failure, URL construction, header forwarding)
- [x] 10.4 Write unit tests for admin model access endpoints (PUT/GET round-trip, 404 for unknown user)
- [x] 10.5 Write unit tests for agent CRUD and agent list filtering
