// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

// Command-line flags.
var (
	httpAddr = flag.String("addr", "localhost:8080", "Listen address")
)

func main() {
	flag.Parse()

	// Initialize Gin router
	r := gin.Default()

	// Prevent caching globally so sensitive tokens/pages aren't stored
	r.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.Next()
	})

	// Serve static files
	r.Static("/static", "./static")

	// Serve favicon from static directory
	r.StaticFile("/favicon.ico", "./static/favicon.ico")

	// Load HTML templates from the templates directory
	r.LoadHTMLGlob("templates/*")

	// Define routes
	r.GET("/", HomeHandler)
	r.POST("/request-login", RequestLoginHandler)
	r.POST("/login", LoginHandler)
	r.GET("/dashboard", DashboardHandler)
	r.GET("/api-docs", ApiDocsHandler)
	// Internal API routes (for dashboard)
	api := r.Group("/api", AuthMiddleware())
	{
		api.GET("/local-data", LocalDataHandler)
		api.GET("/credits/:licenseId", GetCreditsHandler)
		api.POST("/check-credits", CheckCreditsHandler)
		api.POST("/report-usage", ReportUsageHandler)
		api.POST("/update-credits", UpdateCreditsHandler)
		api.DELETE("/delete-credits/:licenseId", DeleteCreditsHandler)

		// New API entries
		api.POST("/create-user", CreateUserHandler)
		api.POST("/delete-user", DeleteUserHandler)

		// Model access management
		api.PUT("/users/:licenseId/models", UpdateModelAccessHandler)
		api.GET("/users/:licenseId/models", GetModelAccessHandler)

		// Agent management
		api.POST("/agents", CreateAgentHandler)
		api.DELETE("/agents/:agentName", DeleteAgentHandler)
	}
	host := os.Getenv("SERVER_HOST")
	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = "8080"
	}
	if host == "" {
		host = "0.0.0.0" // Bind to all interfaces (for IPv4 access)
	}

	tlsCert := os.Getenv("TLS_CERT")
	tlsKey := os.Getenv("TLS_KEY")
	useTLS := tlsCert != "" && tlsKey != ""

	addr := host + ":" + port
	scheme := "http"
	if useTLS {
		scheme = "https"
	}

	// Start the Ollama reverse proxy on a separate port (non-blocking).
	go StartOllamaProxy()

	// Print a clean, informative startup message
	log.Printf("=====================================================")
	log.Printf("Easy AI API Gateway")
	log.Printf("-----------------------------------------------------")
	log.Printf("Local Access:   %s://localhost:%s", scheme, port)
	log.Printf("Network Access: %s://%s:%s", scheme, "your-ip-address", port)
	if useTLS {
		log.Printf("TLS Cert:       %s", tlsCert)
		log.Printf("TLS Key:        %s", tlsKey)
	}
	log.Printf("=====================================================")

	if useTLS {
		if err := r.RunTLS(addr, tlsCert, tlsKey); err != nil {
			log.Fatalf("could not start TLS server: %v", err)
		}
	} else {
		if err := r.Run(addr); err != nil {
			log.Fatalf("could not start server: %v", err)
		}
	}
}

func isDevMode() bool {
	val := os.Getenv("DEV_MODE")
	return val == "true" || val == "1"
}

func HomeHandler(c *gin.Context) {
	if isDevMode() {
		c.Redirect(http.StatusSeeOther, "/dashboard")
		return
	}
	c.HTML(http.StatusOK, "login.html", nil)
}

func RequestLoginHandler(c *gin.Context) {
	adminEmail := os.Getenv("ADMIN_EMAIL")
	if adminEmail == "" {
		log.Println("ADMIN_EMAIL not set")
		c.String(http.StatusInternalServerError, "Server configuration error")
		return
	}

	token, err := generateToken()
	if err != nil {
		log.Println("Error generating token:", err)
		c.String(http.StatusInternalServerError, "Internal Server Error")
		return
	}

	storeToken(adminEmail, token)

	err = sendLoginToken(adminEmail, token)
	if err != nil {
		log.Println("Error sending email:", err)
		c.String(http.StatusInternalServerError, "Failed to send login email")
		return
	}

	c.String(http.StatusOK, "Login token sent to admin email. Please check your inbox.")
}

func LoginHandler(c *gin.Context) {
	token := c.PostForm("token")
	adminEmail := os.Getenv("ADMIN_EMAIL")

	if isValidToken(adminEmail, token) {
		// In a real app, set a secure session cookie here
		// For simplicity, we just redirect. You MUST implement proper sessions for a real app.
		invalidateToken(adminEmail)
		c.Redirect(http.StatusSeeOther, "/dashboard")
		return
	}

	c.String(http.StatusUnauthorized, "Invalid or expired token")
}

func DashboardHandler(c *gin.Context) {
	// Real app needs session validation here!
	primeKey := os.Getenv("PRIME_KEY")
	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"PrimeKey": primeKey,
	})
}

func ApiDocsHandler(c *gin.Context) {
	primeKey := os.Getenv("PRIME_KEY")
	c.HTML(http.StatusOK, "api-docs.html", gin.H{
		"PrimeKey": primeKey,
	})
}

type UserCredits struct {
	LicenseID       string   `json:"licenseId"`
	Email           string   `json:"userEmail"`
	Balance         int      `json:"monthlyToken"`
	CreditsTopup    int      `json:"topUpToken"`
	TokensUsed      int      `json:"usedToken"`
	LastUpdated     int64    `json:"lastUpdated"`
	Application     string   `json:"application"`
	ModelAccessList []string `json:"modelAccessList,omitempty"`
}

// UnmarshalJSON supports both old and new JSON field names for backward compatibility
// with existing encrypted cache files.
func (u *UserCredits) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	getString := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := raw[k]; ok {
				var s string
				if json.Unmarshal(v, &s) == nil {
					return s
				}
			}
		}
		return ""
	}
	getInt := func(keys ...string) int {
		for _, k := range keys {
			if v, ok := raw[k]; ok {
				var n int
				if json.Unmarshal(v, &n) == nil {
					return n
				}
			}
		}
		return 0
	}
	getInt64 := func(keys ...string) int64 {
		for _, k := range keys {
			if v, ok := raw[k]; ok {
				var n int64
				if json.Unmarshal(v, &n) == nil {
					return n
				}
			}
		}
		return 0
	}

	getStringSlice := func(keys ...string) []string {
		for _, k := range keys {
			if v, ok := raw[k]; ok {
				var s []string
				if json.Unmarshal(v, &s) == nil {
					return s
				}
			}
		}
		return nil
	}

	u.LicenseID = getString("licenseId")
	u.Email = getString("userEmail", "email")
	u.Balance = getInt("monthlyToken", "balance")
	u.CreditsTopup = getInt("topUpToken", "creditsTopup")
	u.TokensUsed = getInt("usedToken", "tokensUsed")
	u.LastUpdated = getInt64("lastUpdated")
	u.Application = getString("application")
	u.ModelAccessList = getStringSlice("modelAccessList")
	return nil
}

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("X-API-Key")
		primeKey := os.Getenv("PRIME_KEY")
		if apiKey == "" {
			apiKey = c.Query("api_key") // fallback to query param if needed
		}
		if apiKey != primeKey {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: Invalid API Key"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func LocalDataHandler(c *gin.Context) {
	// For the dashboard to list everything, we only return the local creditsStore
	// No more Firebase connectivity.
	creditsStoreMu.RLock()
	users := make(map[string]*UserCredits, len(creditsStore))
	for k, v := range creditsStore {
		users[k] = v
	}
	creditsStoreMu.RUnlock()
	c.JSON(http.StatusOK, gin.H{"users": users})
}

var creditsStore = make(map[string]*UserCredits)
var creditsStoreMu sync.RWMutex

func init() {
	// Load .env.local early so GOOGLE_APPLICATION_CREDENTIALS (and other vars) are available
	if err := godotenv.Load(".env.local"); err != nil {
		log.Println("init: .env.local not found, relying on environment variables.")
	}
	initFirestore()
	if err := loadFromFirestore(creditsStore); err != nil {
		log.Println("Error loading from Firestore, starting fresh:", err)
	}
	if err := loadAgentsFromFirestore(); err != nil {
		log.Println("Error loading agents from Firestore:", err)
	}
}

func GetCreditsHandler(c *gin.Context) {
	licenseId := c.Param("licenseId")

	creditsStoreMu.RLock()
	credits, ok := creditsStore[licenseId]
	creditsStoreMu.RUnlock()
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "License ID not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"licenseId":      credits.LicenseID,
		"userEmail":      credits.Email,
		"monthlyToken":   credits.Balance,
		"topUpToken":     credits.CreditsTopup,
		"usedToken":      credits.TokensUsed,
		"availableToken": credits.Balance + credits.CreditsTopup - credits.TokensUsed,
		"lastUpdated":    credits.LastUpdated,
		"application":    credits.Application,
	})
}

func CheckCreditsHandler(c *gin.Context) {
	var req struct {
		LicenseID       string `json:"licenseId"`
		EstimatedTokens int    `json:"estimatedTokens"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	creditsStoreMu.RLock()
	credits, ok := creditsStore[req.LicenseID]
	creditsStoreMu.RUnlock()
	if !ok {
		// Default
		credits = &UserCredits{Balance: 1000000}
	}

	available := credits.Balance + credits.CreditsTopup - credits.TokensUsed
	allowed := available >= req.EstimatedTokens
	c.JSON(http.StatusOK, gin.H{
		"allowed":        allowed,
		"availableToken": available,
	})
}

func ReportUsageHandler(c *gin.Context) {
	var req struct {
		LicenseID       string `json:"licenseId"`
		PromptTokens    int    `json:"promptTokens"`
		CandidateTokens int    `json:"candidateTokens"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	totalTokens := req.PromptTokens + req.CandidateTokens
	log.Printf("Reporting local usage for %s: %d tokens", req.LicenseID, totalTokens)

	creditsStoreMu.Lock()
	credits, ok := creditsStore[req.LicenseID]
	if !ok {
		credits = &UserCredits{LicenseID: req.LicenseID, Balance: 1000000}
		creditsStore[req.LicenseID] = credits
	}

	credits.TokensUsed += totalTokens
	credits.LastUpdated = time.Now().UnixMilli()
	creditsStoreMu.Unlock()

	if err := saveUserToFirestore(credits); err != nil {
		log.Println("Error saving to Firestore:", err)
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "tokensUsed": totalTokens, "totalTokensUsed": credits.TokensUsed})
}

func UpdateCreditsHandler(c *gin.Context) {
	var req struct {
		LicenseID    string `json:"licenseId"`
		Email        string `json:"email"`
		Credits      int    `json:"credits"`
		CreditsTopup int    `json:"creditsTopup"`
		Application  string `json:"application"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	isNew := false
	creditsStoreMu.Lock()
	credits, ok := creditsStore[req.LicenseID]
	if !ok {
		credits = &UserCredits{LicenseID: req.LicenseID}
		creditsStore[req.LicenseID] = credits
		isNew = true
	}

	credits.Email = req.Email
	credits.Balance = req.Credits
	credits.CreditsTopup = req.CreditsTopup
	credits.Application = req.Application
	credits.LastUpdated = time.Now().UnixMilli()
	creditsStoreMu.Unlock()

	if err := saveUserToFirestore(credits); err != nil {
		log.Println("Error saving to Firestore after update:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save credits"})
		return
	}

	if isNew {
		sendNewClientEmail(credits.Email, credits.LicenseID, fmt.Sprintf("%d", credits.Balance), credits.Application)
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func CreateUserHandler(c *gin.Context) {
	var req struct {
		LicenseID    string `json:"licenseId"`
		Email        string `json:"email" binding:"required"`
		Credits      int    `json:"credits"`
		CreditsTopup int    `json:"creditsTopup"`
		Application  string `json:"application"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	licenseId := req.LicenseID
	if licenseId == "" {
		licenseId = uuid.New().String()
	}

	credits := &UserCredits{
		LicenseID:    licenseId,
		Email:        req.Email,
		Balance:      req.Credits,
		CreditsTopup: req.CreditsTopup,
		TokensUsed:   0,
		LastUpdated:  time.Now().UnixMilli(),
		Application:  req.Application,
	}

	if credits.Balance == 0 {
		credits.Balance = 1000000 // Default
	}

	creditsStoreMu.Lock()
	creditsStore[licenseId] = credits
	creditsStoreMu.Unlock()

	if err := saveUserToFirestore(credits); err != nil {
		log.Println("Error saving to Firestore after creation:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save new user"})
		return
	}

	// Send welcome email to the user with their License ID and details
	sendNewClientEmail(credits.Email, credits.LicenseID, fmt.Sprintf("%d", credits.Balance), credits.Application)

	c.JSON(http.StatusOK, gin.H{"status": "success", "licenseId": licenseId})
}

func DeleteUserHandler(c *gin.Context) {
	var req struct {
		LicenseID string `json:"licenseId" binding:"required"`
		Email     string `json:"email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	creditsStoreMu.Lock()
	credits, ok := creditsStore[req.LicenseID]
	if !ok || credits.Email != req.Email {
		creditsStoreMu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found with matching License ID and Email"})
		return
	}

	delete(creditsStore, req.LicenseID)
	creditsStoreMu.Unlock()

	if err := deleteUserFromFirestore(req.LicenseID); err != nil {
		log.Println("Error deleting from Firestore:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// UpdateModelAccessHandler handles PUT /api/users/:licenseId/models.
// It updates the user's ModelAccessList in creditsStore and persists to Firestore.
func UpdateModelAccessHandler(c *gin.Context) {
	licenseId := c.Param("licenseId")

	var req struct {
		Models []string `json:"models"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	creditsStoreMu.Lock()
	user, ok := creditsStore[licenseId]
	if !ok {
		creditsStoreMu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	user.ModelAccessList = req.Models
	creditsStoreMu.Unlock()

	if err := saveUserToFirestore(user); err != nil {
		log.Println("Error saving model access to Firestore:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist changes"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// GetModelAccessHandler handles GET /api/users/:licenseId/models.
// It returns the user's current ModelAccessList as a JSON array.
func GetModelAccessHandler(c *gin.Context) {
	licenseId := c.Param("licenseId")

	creditsStoreMu.RLock()
	user, ok := creditsStore[licenseId]
	creditsStoreMu.RUnlock()

	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	models := user.ModelAccessList
	if models == nil {
		models = []string{}
	}

	c.JSON(http.StatusOK, gin.H{"models": models})
}

func DeleteCreditsHandler(c *gin.Context) {
	licenseId := c.Param("licenseId")

	creditsStoreMu.Lock()
	if _, ok := creditsStore[licenseId]; !ok {
		creditsStoreMu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	delete(creditsStore, licenseId)
	creditsStoreMu.Unlock()

	if err := deleteUserFromFirestore(licenseId); err != nil {
		log.Println("Error deleting from Firestore:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}
