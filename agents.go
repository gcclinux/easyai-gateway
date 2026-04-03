package main

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

// Agent represents a named AI agent configuration backed by an Ollama model.
type Agent struct {
	Name         string `json:"name" firestore:"name"`
	Description  string `json:"description" firestore:"description"`
	Model        string `json:"model" firestore:"model"`
	SystemPrompt string `json:"systemPrompt" firestore:"systemPrompt"`
}

var (
	agentsStore   = make(map[string]*Agent)
	agentsStoreMu sync.RWMutex
)

// CreateAgentHandler handles POST /api/agents.
// It accepts a JSON body with name, description, model, and systemPrompt fields,
// saves the agent to the in-memory store and Firestore, and returns success.
func CreateAgentHandler(c *gin.Context) {
	var req struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		Model        string `json:"model"`
		SystemPrompt string `json:"systemPrompt"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	agent := &Agent{
		Name:         req.Name,
		Description:  req.Description,
		Model:        req.Model,
		SystemPrompt: req.SystemPrompt,
	}

	agentsStoreMu.Lock()
	agentsStore[agent.Name] = agent
	agentsStoreMu.Unlock()

	if err := saveAgentToFirestore(agent); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist agent"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// DeleteAgentHandler handles DELETE /api/agents/:agentName.
// It looks up the agent by name, removes it from the in-memory store and
// Firestore, and returns success. Returns 404 if the agent is not found.
func DeleteAgentHandler(c *gin.Context) {
	agentName := c.Param("agentName")

	agentsStoreMu.Lock()
	_, ok := agentsStore[agentName]
	if !ok {
		agentsStoreMu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "agent not found"})
		return
	}
	delete(agentsStore, agentName)
	agentsStoreMu.Unlock()

	if err := deleteAgentFromFirestore(agentName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete agent"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// filterAgents filters agents by a user's ModelAccessList.
// If accessList is empty or nil, all agents are returned.
// If accessList is non-empty, only agents whose Model is in the accessList are returned.
func filterAgents(agents map[string]*Agent, accessList []string) []*Agent {
	if len(accessList) == 0 {
		result := make([]*Agent, 0, len(agents))
		for _, a := range agents {
			result = append(result, a)
		}
		return result
	}

	allowed := make(map[string]bool, len(accessList))
	for _, model := range accessList {
		allowed[model] = true
	}

	var result []*Agent
	for _, a := range agents {
		if allowed[a.Model] {
			result = append(result, a)
		}
	}
	return result
}

// AgentsListHandler returns a Gin handler for GET /api/agents on the proxy.
// It reads the agents store, filters by the authenticated user's ModelAccessList,
// and returns the filtered list as a JSON array.
func AgentsListHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get the authenticated user from context.
		var accessList []string
		if userVal, exists := c.Get("user"); exists {
			if user, ok := userVal.(*UserCredits); ok {
				accessList = user.ModelAccessList
			}
		}

		agentsStoreMu.RLock()
		agents := filterAgents(agentsStore, accessList)
		agentsStoreMu.RUnlock()

		c.JSON(http.StatusOK, agents)
	}
}
