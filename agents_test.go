package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// --- Tests for agent CRUD and list filtering (Task 10.5) ---

func setupAgentAdminRouter() *gin.Engine {
	engine := gin.New()
	engine.Use(gin.Recovery())
	api := engine.Group("/api", AuthMiddleware())
	{
		api.POST("/agents", CreateAgentHandler)
		api.DELETE("/agents/:agentName", DeleteAgentHandler)
	}
	return engine
}

func TestCreateAgent_Success(t *testing.T) {
	primeKey := "test-prime-key-agents"
	t.Setenv("PRIME_KEY", primeKey)

	// Clean up any leftover agents
	agentsStoreMu.Lock()
	delete(agentsStore, "test-agent")
	agentsStoreMu.Unlock()
	defer func() {
		agentsStoreMu.Lock()
		delete(agentsStore, "test-agent")
		agentsStoreMu.Unlock()
	}()

	engine := setupAgentAdminRouter()

	body, _ := json.Marshal(map[string]interface{}{
		"name":         "test-agent",
		"description":  "A test agent",
		"model":        "llama3:latest",
		"systemPrompt": "You are a test assistant.",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", primeKey)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	// The handler saves to Firestore which may fail in tests, but the in-memory store is updated first.
	// Accept either 200 (Firestore works) or 500 (Firestore fails but in-memory updated).
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 200 or 500, got %d: %s", w.Code, w.Body.String())
	}

	// Verify in-memory store was updated
	agentsStoreMu.RLock()
	agent, ok := agentsStore["test-agent"]
	agentsStoreMu.RUnlock()
	if !ok {
		t.Fatal("agent should exist in agentsStore")
	}
	if agent.Model != "llama3:latest" {
		t.Errorf("expected model llama3:latest, got %s", agent.Model)
	}
	if agent.Description != "A test agent" {
		t.Errorf("expected description 'A test agent', got %s", agent.Description)
	}
}

func TestCreateAgent_MissingName(t *testing.T) {
	primeKey := "test-prime-key-agents"
	t.Setenv("PRIME_KEY", primeKey)

	engine := setupAgentAdminRouter()

	body, _ := json.Marshal(map[string]interface{}{
		"description": "No name agent",
		"model":       "llama3:latest",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/agents", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", primeKey)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestDeleteAgent_Success(t *testing.T) {
	primeKey := "test-prime-key-agents"
	t.Setenv("PRIME_KEY", primeKey)

	// Pre-populate the agent
	agentsStoreMu.Lock()
	agentsStore["delete-me"] = &Agent{Name: "delete-me", Model: "llama3:latest"}
	agentsStoreMu.Unlock()
	defer func() {
		agentsStoreMu.Lock()
		delete(agentsStore, "delete-me")
		agentsStoreMu.Unlock()
	}()

	engine := setupAgentAdminRouter()

	req := httptest.NewRequest(http.MethodDelete, "/api/agents/delete-me", nil)
	req.Header.Set("X-API-Key", primeKey)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	// Accept 200 or 500 (Firestore may fail in test env)
	if w.Code != http.StatusOK && w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 200 or 500, got %d: %s", w.Code, w.Body.String())
	}

	// Verify agent was removed from in-memory store
	agentsStoreMu.RLock()
	_, ok := agentsStore["delete-me"]
	agentsStoreMu.RUnlock()
	if ok {
		t.Error("agent should have been deleted from agentsStore")
	}
}

func TestDeleteAgent_NotFound(t *testing.T) {
	primeKey := "test-prime-key-agents"
	t.Setenv("PRIME_KEY", primeKey)

	engine := setupAgentAdminRouter()

	req := httptest.NewRequest(http.MethodDelete, "/api/agents/nonexistent-agent", nil)
	req.Header.Set("X-API-Key", primeKey)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestAgentsListHandler_ReturnsFilteredAgents(t *testing.T) {
	// Set up agents in the store
	agentsStoreMu.Lock()
	agentsStore["agent-llama"] = &Agent{Name: "agent-llama", Model: "llama3:latest", Description: "Llama agent"}
	agentsStore["agent-mistral"] = &Agent{Name: "agent-mistral", Model: "mistral:latest", Description: "Mistral agent"}
	agentsStore["agent-code"] = &Agent{Name: "agent-code", Model: "codellama:latest", Description: "Code agent"}
	agentsStoreMu.Unlock()
	defer func() {
		agentsStoreMu.Lock()
		delete(agentsStore, "agent-llama")
		delete(agentsStore, "agent-mistral")
		delete(agentsStore, "agent-code")
		agentsStoreMu.Unlock()
	}()

	user := &UserCredits{ModelAccessList: []string{"llama3:latest", "codellama:latest"}}

	w := httptest.NewRecorder()
	_, engine := gin.CreateTestContext(w)
	engine.Use(func(c *gin.Context) {
		c.Set("user", user)
		c.Next()
	})
	engine.GET("/api/agents", AgentsListHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var agents []*Agent
	json.Unmarshal(w.Body.Bytes(), &agents)
	if len(agents) != 2 {
		t.Errorf("expected 2 filtered agents, got %d", len(agents))
	}
	for _, a := range agents {
		if a.Model != "llama3:latest" && a.Model != "codellama:latest" {
			t.Errorf("unexpected agent model: %s", a.Model)
		}
	}
}

func TestAgentsListHandler_ReturnsAllWhenNoAccessList(t *testing.T) {
	agentsStoreMu.Lock()
	agentsStore["agent-all-1"] = &Agent{Name: "agent-all-1", Model: "llama3:latest"}
	agentsStore["agent-all-2"] = &Agent{Name: "agent-all-2", Model: "mistral:latest"}
	agentsStoreMu.Unlock()
	defer func() {
		agentsStoreMu.Lock()
		delete(agentsStore, "agent-all-1")
		delete(agentsStore, "agent-all-2")
		agentsStoreMu.Unlock()
	}()

	user := &UserCredits{ModelAccessList: nil}

	w := httptest.NewRecorder()
	_, engine := gin.CreateTestContext(w)
	engine.Use(func(c *gin.Context) {
		c.Set("user", user)
		c.Next()
	})
	engine.GET("/api/agents", AgentsListHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var agents []*Agent
	json.Unmarshal(w.Body.Bytes(), &agents)
	if len(agents) < 2 {
		t.Errorf("expected at least 2 agents, got %d", len(agents))
	}
}

// Feature: ollama-proxy-auth, Property 13: Agent Filtering by Model Access
func TestProperty_FilterAgents(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = 100
	properties := gopter.NewProperties(parameters)

	// **Validates: Requirements 6.3**
	properties.Property("empty access list returns all agents", prop.ForAll(
		func(agentNames []string, models []string) bool {
			agents := make(map[string]*Agent)
			for i, name := range agentNames {
				model := ""
				if i < len(models) {
					model = models[i]
				}
				agents[name] = &Agent{Name: name, Model: model}
			}
			result := filterAgents(agents, nil)
			return len(result) == len(agents)
		},
		gen.SliceOfN(5, gen.AlphaString().SuchThat(func(s string) bool { return len(s) > 0 })),
		gen.SliceOfN(5, gen.AlphaString().SuchThat(func(s string) bool { return len(s) > 0 })),
	))

	properties.Property("non-empty access list returns only agents with allowed models", prop.ForAll(
		func(agentModels []string, accessList []string) bool {
			if len(accessList) == 0 || len(agentModels) == 0 {
				return true // skip degenerate cases
			}
			agents := make(map[string]*Agent)
			for i, model := range agentModels {
				name := "agent-" + model + "-" + string(rune('A'+i))
				agents[name] = &Agent{Name: name, Model: model}
			}

			allowed := make(map[string]bool, len(accessList))
			for _, m := range accessList {
				allowed[m] = true
			}

			result := filterAgents(agents, accessList)

			// Every returned agent must have a model in the access list
			for _, a := range result {
				if !allowed[a.Model] {
					return false
				}
			}

			// Every agent with an allowed model must be in the result
			resultNames := make(map[string]bool)
			for _, a := range result {
				resultNames[a.Name] = true
			}
			for _, a := range agents {
				if allowed[a.Model] && !resultNames[a.Name] {
					return false
				}
			}

			return true
		},
		gen.SliceOfN(10, gen.AlphaString().SuchThat(func(s string) bool { return len(s) > 0 })),
		gen.SliceOfN(3, gen.AlphaString().SuchThat(func(s string) bool { return len(s) > 0 })),
	))

	properties.Property("empty access list with empty slice returns all agents", prop.ForAll(
		func(agentNames []string) bool {
			agents := make(map[string]*Agent)
			for _, name := range agentNames {
				agents[name] = &Agent{Name: name, Model: "some-model"}
			}
			result := filterAgents(agents, []string{})
			return len(result) == len(agents)
		},
		gen.SliceOfN(5, gen.AlphaString().SuchThat(func(s string) bool { return len(s) > 0 })),
	))

	properties.TestingRun(t)
}
