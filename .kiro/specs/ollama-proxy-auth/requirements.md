# Requirements Document

## Introduction

This feature adds an authenticated reverse proxy to the easyai-gateway application, enabling controlled access to a locally running Ollama AI server. Currently, Ollama is exposed directly on port 11434 without any authentication. The proxy will sit in front of Ollama, validate user API keys against Firestore, enforce per-model access control, and forward authorized traffic to the internal Ollama instance. This allows the gateway to manage who can use which AI models while keeping the Ollama server itself inaccessible from the outside.

## Glossary

- **Ollama_Proxy**: The reverse proxy component within easyai-gateway that listens on the external Ollama port (default 11434), authenticates incoming requests, and forwards valid traffic to the internal Ollama server.
- **Ollama_Server**: The local Ollama AI inference server, reconfigured to listen on an internal-only port not exposed to external networks.
- **User**: A registered entity in Firestore with a licenseId (API key), email, credits, and model access permissions.
- **License_ID**: A unique identifier (UUID) that serves as the User's API key for authenticating with the Ollama_Proxy.
- **Model_Access_List**: A per-User list of Ollama model names that the User is authorized to use.
- **Credits_Store**: The in-memory map and Firestore "credits" collection that holds User records.
- **Agent**: A named configuration combining a specific Ollama model with optional system prompts or parameters, exposed to Users through the gateway.
- **Admin**: The operator who manages Users, model access, and Agent configurations via the existing admin API (authenticated with PRIME_KEY).

## Requirements

### Requirement 1: Proxy Server Startup

**User Story:** As an Admin, I want the gateway to start a reverse proxy listener on the external Ollama port, so that all Ollama traffic is routed through the gateway for authentication.

#### Acceptance Criteria

1. WHEN the gateway application starts, THE Ollama_Proxy SHALL listen on the host and port specified by the OLLAMA_PROXY_HOST and OLLAMA_PROXY_PORT environment variables.
2. WHEN OLLAMA_PROXY_PORT is not set, THE Ollama_Proxy SHALL default to port 11434.
3. WHEN OLLAMA_PROXY_HOST is not set, THE Ollama_Proxy SHALL default to binding on all interfaces (0.0.0.0).
4. THE Ollama_Proxy SHALL forward authorized requests to the Ollama_Server at the address specified by the OLLAMA_INTERNAL_URL environment variable.
5. WHEN OLLAMA_INTERNAL_URL is not set, THE Ollama_Proxy SHALL default to http://localhost:11435.
6. THE Ollama_Proxy SHALL run concurrently alongside the existing admin API server without blocking either server.

### Requirement 2: API Key Authentication

**User Story:** As a User, I want to authenticate my Ollama requests using my License_ID, so that only authorized users can access the AI models.

#### Acceptance Criteria

1. WHEN a request arrives at the Ollama_Proxy, THE Ollama_Proxy SHALL extract the API key from the Authorization header using the Bearer scheme.
2. WHEN no Authorization header is present, THE Ollama_Proxy SHALL check for the API key in the X-API-Key request header.
3. WHEN no API key is found in any supported location, THE Ollama_Proxy SHALL respond with HTTP 401 and a JSON body containing an error message.
4. WHEN an API key is provided, THE Ollama_Proxy SHALL look up the key as a License_ID in the Credits_Store.
5. WHEN the License_ID is not found in the Credits_Store, THE Ollama_Proxy SHALL respond with HTTP 403 and a JSON body containing an "access denied" error message.
6. WHEN the License_ID is found and the User has available credits (monthlyToken + topUpToken - usedToken > 0), THE Ollama_Proxy SHALL forward the request to the Ollama_Server.
7. WHEN the User has zero or negative available credits, THE Ollama_Proxy SHALL respond with HTTP 403 and a JSON body indicating insufficient credits.

### Requirement 3: Model-Level Access Control

**User Story:** As an Admin, I want to restrict which models each User can access, so that I can control resource usage and offer tiered service levels.

#### Acceptance Criteria

1. THE Credits_Store SHALL store a Model_Access_List field for each User record.
2. WHEN a User's Model_Access_List is empty or not set, THE Ollama_Proxy SHALL allow access to all available models on the Ollama_Server.
3. WHEN a request includes a model name in the JSON body, THE Ollama_Proxy SHALL extract the model name and check it against the User's Model_Access_List.
4. WHEN the requested model is not in the User's Model_Access_List and the list is non-empty, THE Ollama_Proxy SHALL respond with HTTP 403 and a JSON body indicating the User is not authorized for that model.
5. WHEN the requested model is in the User's Model_Access_List, THE Ollama_Proxy SHALL forward the request to the Ollama_Server.
6. WHEN the request is a model listing endpoint (GET /api/tags), THE Ollama_Proxy SHALL forward the response from the Ollama_Server and filter the results to include only models present in the User's Model_Access_List.
7. WHEN the User's Model_Access_List is empty or not set, THE Ollama_Proxy SHALL return the full unfiltered model list from the Ollama_Server.

### Requirement 4: Request Proxying

**User Story:** As a User, I want all Ollama API endpoints to work transparently through the proxy, so that I can use my existing Ollama-compatible tools without modification (other than adding authentication).

#### Acceptance Criteria

1. THE Ollama_Proxy SHALL forward all HTTP methods (GET, POST, PUT, DELETE) to the Ollama_Server.
2. THE Ollama_Proxy SHALL preserve the original request path, query parameters, and request body when forwarding to the Ollama_Server.
3. THE Ollama_Proxy SHALL preserve the original request headers (except authentication headers consumed by the proxy) when forwarding to the Ollama_Server.
4. THE Ollama_Proxy SHALL stream response bodies from the Ollama_Server back to the client without buffering, to support streaming chat completions.
5. THE Ollama_Proxy SHALL preserve the HTTP status code from the Ollama_Server response.
6. THE Ollama_Proxy SHALL preserve response headers from the Ollama_Server.
7. IF the Ollama_Server is unreachable, THEN THE Ollama_Proxy SHALL respond with HTTP 502 and a JSON body indicating the upstream server is unavailable.

### Requirement 5: Admin API for Model Access Management

**User Story:** As an Admin, I want API endpoints to manage per-User model access lists, so that I can grant or revoke access to specific models.

#### Acceptance Criteria

1. WHEN an Admin sends a PUT request to /api/users/{licenseId}/models with a JSON body containing a list of model names, THE Admin_API SHALL update the User's Model_Access_List in both the Credits_Store and Firestore.
2. WHEN the specified License_ID does not exist, THE Admin_API SHALL respond with HTTP 404 and a JSON body indicating the User was not found.
3. WHEN an Admin sends a GET request to /api/users/{licenseId}/models, THE Admin_API SHALL return the User's current Model_Access_List as a JSON array.
4. THE Admin_API SHALL require the existing PRIME_KEY authentication (X-API-Key header) for all model access management endpoints.

### Requirement 6: Agents and LLM Listing Endpoint

**User Story:** As a User, I want to see which Agents and LLMs are available to me, so that I can choose the right model for my task.

#### Acceptance Criteria

1. WHEN an authenticated User sends a GET request to /api/agents, THE Ollama_Proxy SHALL return a JSON list of Agents configured in the system.
2. THE Agent list response SHALL include for each Agent: name, description, underlying model name, and any configured system prompt.
3. WHEN the User has a non-empty Model_Access_List, THE Ollama_Proxy SHALL filter the Agent list to include only Agents whose underlying model is in the User's Model_Access_List.
4. THE Admin_API SHALL provide a POST /api/agents endpoint to create a new Agent, accepting name, description, model, and system prompt fields.
5. THE Admin_API SHALL provide a DELETE /api/agents/{agentName} endpoint to remove an Agent.
6. THE Admin_API SHALL store Agent configurations in Firestore in an "agents" collection.
7. THE Admin_API SHALL require PRIME_KEY authentication for Agent creation and deletion endpoints.

### Requirement 7: Usage Tracking Through Proxy

**User Story:** As an Admin, I want Ollama usage through the proxy to be tracked against User credits, so that I can monitor and limit resource consumption.

#### Acceptance Criteria

1. WHEN the Ollama_Server returns a response that includes token usage information (eval_count, prompt_eval_count fields), THE Ollama_Proxy SHALL extract the token counts from the response.
2. WHEN token usage is extracted, THE Ollama_Proxy SHALL update the User's usedToken count in the Credits_Store and persist the change to Firestore.
3. WHEN the Ollama_Server response is streamed, THE Ollama_Proxy SHALL extract token usage from the final chunk of the stream (which contains the usage summary).
4. IF token usage information is not present in the response, THEN THE Ollama_Proxy SHALL forward the response without modifying the User's credit balance.

### Requirement 8: Logging and Observability

**User Story:** As an Admin, I want proxy requests to be logged, so that I can monitor usage patterns and troubleshoot issues.

#### Acceptance Criteria

1. THE Ollama_Proxy SHALL log each incoming request with: timestamp, User License_ID (masked to show only last 4 characters), requested model, request path, and HTTP method.
2. THE Ollama_Proxy SHALL log each completed request with: response status code and request duration.
3. WHEN authentication fails, THE Ollama_Proxy SHALL log the failure reason and the client IP address.
4. THE Ollama_Proxy SHALL use the same logging mechanism (Go standard log package) as the existing gateway application.
