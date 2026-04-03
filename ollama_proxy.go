package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// maskLicenseID masks all but the last 4 characters of a license ID with
// asterisks. For strings shorter than 4 characters, the entire string is
// masked. An empty string returns an empty string.
func maskLicenseID(id string) string {
	if len(id) == 0 {
		return ""
	}
	if len(id) < 4 {
		return strings.Repeat("*", len(id))
	}
	return strings.Repeat("*", len(id)-4) + id[len(id)-4:]
}

// extractAPIKey extracts an API key from the given header values.
// It first checks the Authorization header for a "Bearer <key>" value;
// if not present, it falls back to the X-API-Key header value.
func extractAPIKey(authHeader string, xAPIKeyHeader string) string {
	if authHeader != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(authHeader, prefix) {
			return strings.TrimSpace(authHeader[len(prefix):])
		}
	}
	return strings.TrimSpace(xAPIKeyHeader)
}

// checkCredits returns true if the user has available credits
// (monthlyToken + topUpToken - usedToken > 0).
func checkCredits(user *UserCredits) bool {
	if user == nil {
		return false
	}
	return user.Balance+user.CreditsTopup-user.TokensUsed > 0
}

// checkModelAccess returns true if the given model is allowed by the access list.
// If accessList is empty or nil, all models are allowed.
// If accessList is non-empty, the model must be present in the list.
func checkModelAccess(model string, accessList []string) bool {
	if len(accessList) == 0 {
		return true
	}
	for _, allowed := range accessList {
		if allowed == model {
			return true
		}
	}
	return false
}

// OllamaProxyAuthMiddleware returns a Gin middleware that authenticates
// requests using the creditsStore. It extracts the API key, validates it
// against known license IDs, checks credits, and sets "licenseId" and
// "user" in the Gin context on success.
func OllamaProxyAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := extractAPIKey(c.GetHeader("Authorization"), c.GetHeader("X-API-Key"))
		if key == "" {
			log.Printf("auth failed: no API key provided, client IP: %s", c.ClientIP())
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized: API key required"})
			c.Abort()
			return
		}

		creditsStoreMu.RLock()
		user, ok := creditsStore[key]
		creditsStoreMu.RUnlock()

		if !ok {
			log.Printf("auth failed: invalid API key, client IP: %s", c.ClientIP())
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied: invalid API key"})
			c.Abort()
			return
		}

		if !checkCredits(user) {
			log.Printf("auth failed: insufficient credits for %s, client IP: %s", maskLicenseID(key), c.ClientIP())
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient credits"})
			c.Abort()
			return
		}

		c.Set("licenseId", key)
		c.Set("user", user)
		c.Next()
	}
}

// ProxyConfig holds the resolved configuration for the Ollama reverse proxy.
type ProxyConfig struct {
	Host        string
	Port        string
	InternalURL string
}

// resolveProxyConfig reads proxy configuration from environment variables,
// falling back to defaults when not set.
func resolveProxyConfig() ProxyConfig {
	host := os.Getenv("OLLAMA_PROXY_HOST")
	if host == "" {
		host = "0.0.0.0"
	}
	port := os.Getenv("OLLAMA_PROXY_PORT")
	if port == "" {
		port = "11434"
	}
	internalURL := os.Getenv("OLLAMA_INTERNAL_URL")
	if internalURL == "" {
		internalURL = "http://localhost:11435"
	}
	return ProxyConfig{
		Host:        host,
		Port:        port,
		InternalURL: internalURL,
	}
}

// RequestLoggingMiddleware returns a Gin middleware that logs incoming requests
// and their completion. It logs method, path, masked license ID, and model on
// entry, and status code with duration on completion.
func RequestLoggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		duration := time.Since(start)
		statusCode := c.Writer.Status()

		licenseId, _ := c.Get("licenseId")
		masked := ""
		if id, ok := licenseId.(string); ok && id != "" {
			masked = maskLicenseID(id)
		}

		model, _ := c.Get("requestModel")
		modelStr := ""
		if m, ok := model.(string); ok {
			modelStr = m
		}

		log.Printf("proxy request: method=%s path=%s license=%s model=%s status=%d duration=%s",
			c.Request.Method, c.Request.URL.Path, masked, modelStr, statusCode, duration)
	}
}

// StartOllamaProxy initializes and starts the Ollama reverse proxy server.
// It should be called as a goroutine from main().
func StartOllamaProxy() {
	cfg := resolveProxyConfig()

	engine := gin.Default()

	// Health check endpoint (unauthenticated).
	engine.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Shared middleware stack for authenticated proxy routes.
	authMiddleware := []gin.HandlerFunc{
		OllamaProxyAuthMiddleware(),
		ModelAccessMiddleware(),
		RequestLoggingMiddleware(),
	}

	// Specific routes that need special handling.
	tagsGroup := engine.Group("/", authMiddleware...)
	{
		tagsGroup.GET("/api/tags", TagsFilterHandler())
		tagsGroup.GET("/api/agents", AgentsListHandler())
	}

	// Catch-all: use NoRoute so it doesn't conflict with the specific routes above.
	proxyHandler := ProxyHandler()
	engine.NoRoute(func(c *gin.Context) {
		// Apply auth middleware chain manually for the catch-all.
		for _, mw := range authMiddleware {
			mw(c)
			if c.IsAborted() {
				return
			}
		}
		proxyHandler(c)
	})

	addr := cfg.Host + ":" + cfg.Port
	log.Printf("=====================================================")
	log.Printf("Ollama Proxy")
	log.Printf("-----------------------------------------------------")
	log.Printf("Listening on: %s", addr)
	log.Printf("Upstream:     %s", cfg.InternalURL)
	log.Printf("=====================================================")

	if err := engine.Run(addr); err != nil {
		log.Fatalf("Ollama proxy failed to start: %v", err)
	}
}

// ModelAccessMiddleware returns a Gin middleware that checks whether the
// authenticated user is allowed to access the requested model.
// For POST/PUT/PATCH requests it reads the JSON body, extracts the "model"
// field, and validates it against the user's ModelAccessList.
// The request body is restored so downstream handlers can read it again.
// GET requests and requests without a model field pass through.
func ModelAccessMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only inspect body for methods that typically carry a JSON payload.
		method := c.Request.Method
		if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch {
			c.Next()
			return
		}

		// Read the full body so we can inspect it and restore it.
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			c.Abort()
			return
		}
		// Restore the body immediately for downstream handlers.
		c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		// If the body is empty, nothing to check.
		if len(bodyBytes) == 0 {
			c.Next()
			return
		}

		// Try to extract the "model" field from the JSON body.
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			// Body isn't valid JSON or doesn't have a model field — pass through.
			c.Next()
			return
		}

		if payload.Model == "" {
			c.Next()
			return
		}

		// Retrieve the authenticated user from context (set by OllamaProxyAuthMiddleware).
		userVal, exists := c.Get("user")
		if !exists {
			c.Next()
			return
		}
		user, ok := userVal.(*UserCredits)
		if !ok {
			c.Next()
			return
		}

		// Store the model name in context for logging middleware.
		c.Set("requestModel", payload.Model)

		if !checkModelAccess(payload.Model, user.ModelAccessList) {
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied: model not authorized"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// filterModels filters a slice of model maps, returning only those whose
// "name" field is present in the given accessList. If accessList is empty
// or nil, the full list is returned unmodified.
func filterModels(models []map[string]interface{}, accessList []string) []map[string]interface{} {
	if len(accessList) == 0 {
		return models
	}
	allowed := make(map[string]bool, len(accessList))
	for _, name := range accessList {
		allowed[name] = true
	}
	var filtered []map[string]interface{}
	for _, m := range models {
		name, ok := m["name"]
		if !ok {
			continue
		}
		nameStr, ok := name.(string)
		if !ok {
			continue
		}
		if allowed[nameStr] {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

// TagsFilterHandler returns a Gin handler for GET /api/tags that forwards
// the request to the upstream Ollama server and filters the returned model
// list based on the authenticated user's ModelAccessList.
func TagsFilterHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := resolveProxyConfig()

		// Forward the request to the upstream Ollama server.
		upstreamURL := strings.TrimRight(cfg.InternalURL, "/") + "/api/tags"
		resp, err := http.Get(upstreamURL)
		if err != nil {
			log.Printf("TagsFilterHandler: upstream request failed: %v", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "upstream server unavailable"})
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("TagsFilterHandler: failed to read upstream response: %v", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "upstream server unavailable"})
			return
		}

		// If upstream returned a non-200 status, forward it as-is.
		if resp.StatusCode != http.StatusOK {
			c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), body)
			return
		}

		// Parse the Ollama tags response.
		var tagsResp map[string]interface{}
		if err := json.Unmarshal(body, &tagsResp); err != nil {
			// Can't parse — return raw response.
			c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), body)
			return
		}

		// Extract the models array.
		modelsRaw, ok := tagsResp["models"]
		if !ok {
			c.JSON(http.StatusOK, tagsResp)
			return
		}

		// Convert to []map[string]interface{} for filtering.
		modelsJSON, err := json.Marshal(modelsRaw)
		if err != nil {
			c.JSON(http.StatusOK, tagsResp)
			return
		}
		var models []map[string]interface{}
		if err := json.Unmarshal(modelsJSON, &models); err != nil {
			c.JSON(http.StatusOK, tagsResp)
			return
		}

		// Get the user's access list from context.
		var accessList []string
		if userVal, exists := c.Get("user"); exists {
			if user, ok := userVal.(*UserCredits); ok {
				accessList = user.ModelAccessList
			}
		}

		filtered := filterModels(models, accessList)
		tagsResp["models"] = filtered

		c.JSON(http.StatusOK, tagsResp)
	}
}

// extractTokenUsage parses a JSON object looking for done, eval_count, and
// prompt_eval_count fields. It returns the values found (0 for missing numeric
// fields, false for missing done). Malformed JSON returns zeros gracefully.
func extractTokenUsage(data []byte) (evalCount int, promptEvalCount int, done bool) {
	var obj struct {
		Done            bool `json:"done"`
		EvalCount       int  `json:"eval_count"`
		PromptEvalCount int  `json:"prompt_eval_count"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return 0, 0, false
	}
	return obj.EvalCount, obj.PromptEvalCount, obj.Done
}

// ProxyHandler returns a Gin handler that forwards requests to the upstream
// Ollama server. It builds the upstream URL, copies the request (method, path,
// query, body, non-auth headers), streams the response back using
// bufio.Scanner + http.Flusher for NDJSON support, extracts token usage from
// the final chunk, and updates user credits accordingly.
func ProxyHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := resolveProxyConfig()

		// Build upstream URL: internal URL + original path + query string.
		upstreamURL := strings.TrimRight(cfg.InternalURL, "/") + c.Request.URL.Path
		if c.Request.URL.RawQuery != "" {
			upstreamURL += "?" + c.Request.URL.RawQuery
		}

		// Create a new request to the upstream server.
		upstreamReq, err := http.NewRequestWithContext(
			c.Request.Context(),
			c.Request.Method,
			upstreamURL,
			c.Request.Body,
		)
		if err != nil {
			log.Printf("ProxyHandler: failed to create upstream request: %v", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "upstream server unavailable"})
			return
		}

		// Copy all request headers except Authorization and X-API-Key.
		for key, values := range c.Request.Header {
			keyLower := strings.ToLower(key)
			if keyLower == "authorization" || keyLower == "x-api-key" {
				continue
			}
			for _, v := range values {
				upstreamReq.Header.Add(key, v)
			}
		}

		// Use an http.Client with no timeout for streaming support.
		client := &http.Client{Timeout: 0}
		resp, err := client.Do(upstreamReq)
		if err != nil {
			log.Printf("ProxyHandler: upstream request failed: %v", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "upstream server unavailable"})
			return
		}
		defer resp.Body.Close()

		// Copy response headers from upstream to client.
		for key, values := range resp.Header {
			for _, v := range values {
				c.Writer.Header().Add(key, v)
			}
		}

		// Write the upstream status code.
		c.Writer.WriteHeader(resp.StatusCode)

		// Stream the response back using bufio.Scanner for NDJSON support.
		flusher, canFlush := c.Writer.(http.Flusher)
		scanner := bufio.NewScanner(resp.Body)

		// Increase scanner buffer for potentially large lines.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		var totalTokens int
		var tokensFound bool

		for scanner.Scan() {
			line := scanner.Bytes()

			// Inspect each line for token usage.
			evalCount, promptEvalCount, done := extractTokenUsage(line)
			if done {
				totalTokens = evalCount + promptEvalCount
				tokensFound = true
			}

			// Write the line to the client, followed by a newline.
			c.Writer.Write(line)
			c.Writer.Write([]byte("\n"))
			if canFlush {
				flusher.Flush()
			}
		}

		// After streaming completes, update user credits if tokens were captured.
		if tokensFound && totalTokens > 0 {
			if userVal, exists := c.Get("user"); exists {
				if user, ok := userVal.(*UserCredits); ok {
					creditsStoreMu.Lock()
					user.TokensUsed += totalTokens
					user.LastUpdated = time.Now().UnixMilli()
					creditsStoreMu.Unlock()

					// Save to Firestore asynchronously.
					go saveUserToFirestore(user)
				}
			}
		}
	}
}
