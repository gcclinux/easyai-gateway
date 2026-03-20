# External App Integration Guide: EasyAI Standalone API Gateway

This guide explains how to integrate your external application with the `easyai-gateway` gateway to track token consumption and manage user credits in a 100% standalone environment.

## 0. Authentication

All API requests MUST include the logic of a valid API Key in the request header. This key must match the `PRIME_KEY` defined in the gateway's `.env.local` file.

**Header Name**: `X-API-Key`  
**Example Value**: `xxxxxx-xxxxxx-xxxxxx-xxxxxx`

---

## 1. Create User (Onboarding)

Onboard a new user into the local encrypted storage. This generates a unique `licenseId` for the user.

**Endpoint**: `POST /api/create-user`

**Payload**:
```json
{
  "licenseId": "OPTIONAL_CUSTOM_ID",
  "email": "user@example.com",
  "credits": 1000000,
  "creditsTopup": 0,
  "application": "EasyAI"
}
```

> [!TIP]
> If `licenseId` is not provided, the gateway will automatically generate a unique UUID for the user.

**curl Example**:
```bash
curl -X POST http://localhost:8080/api/create-user \
     -H "Content-Type: application/json" \
     -H "X-API-Key: YOUR_PRIME_KEY" \
     -d '{"licenseId": "USER-STORES-THIS-KEY", "email": "user@example.com", "credits": 1000000, "application": "EasyAI"}'
```

**PowerShell Example**:
```powershell
$body = @{
    licenseId = "USER-STORES-THIS-KEY"
    email = "user@example.com"
    credits = 1000000
    application = "EasyAI"
} | ConvertTo-Json

Invoke-RestMethod -Uri "http://localhost:8080/api/create-user" -Method Post -Body $body -ContentType "application/json" -Headers @{"X-API-Key"="YOUR_PRIME_KEY"}
```

---

## 2. Check Credits

Before calling Gemini or any LLM, verify if the user has enough tokens.

**Endpoint**: `POST /api/check-credits`

**Payload**:
```json
{
  "licenseId": "USER_LICENSE_ID",
  "estimatedTokens": 500
}
```

**curl Example**:
```bash
curl -X POST http://localhost:8080/api/check-credits \
     -H "Content-Type: application/json" \
     -H "X-API-Key: YOUR_PRIME_KEY" \
     -d '{"licenseId": "8f8e8c8a-...", "estimatedTokens": 500}'
```

**PowerShell Example**:
```powershell
$body = @{
    licenseId = "USER_LICENSE_ID"
    estimatedTokens = 500
} | ConvertTo-Json

$resp = Invoke-RestMethod -Uri "http://localhost:8080/api/check-credits" -Method Post -Body $body -ContentType "application/json" -Headers @{"X-API-Key"="YOUR_PRIME_KEY"}

if ($resp.allowed) {
    Write-Host "Proceed. Remaining: $($resp.remaining)"
}
```

---

## 3. Report Usage

After receiving a response from the LLM, report the actual tokens used.

**Endpoint**: `POST /api/report-usage`

**Payload**:
```json
{
  "licenseId": "USER_LICENSE_ID",
  "promptTokens": 12,
  "candidateTokens": 45
}
```

**curl Example**:
```bash
curl -X POST http://localhost:8080/api/report-usage \
     -H "Content-Type: application/json" \
     -H "X-API-Key: YOUR_PRIME_KEY" \
     -d '{"licenseId": "USER_LICENSE_ID", "promptTokens": 12, "candidateTokens": 45}'
```

**PowerShell Example**:
```powershell
$usage = @{
    licenseId = "USER_LICENSE_ID"
    promptTokens = 12
    candidateTokens = 45
} | ConvertTo-Json

Invoke-RestMethod -Uri "http://localhost:8080/api/report-usage" -Method Post -Body $usage -ContentType "application/json" -Headers @{"X-API-Key"="YOUR_PRIME_KEY"}
```

---

## 4. Delete User

Remove a user from the local storage. Both `licenseId` and `email` must match for security.

**Endpoint**: `POST /api/delete-user`

**Payload**:
```json
{
  "licenseId": "USER_LICENSE_ID",
  "email": "user@example.com"
}
```

**curl Example**:
```bash
curl -X POST http://localhost:8080/api/delete-user \
     -H "Content-Type: application/json" \
     -H "X-API-Key: YOUR_PRIME_KEY" \
     -d '{"licenseId": "USER_LICENSE_ID", "email": "user@example.com"}'
```

**PowerShell Example**:
```powershell
$del = @{
    licenseId = "USER_LICENSE_ID"
    email = "user@example.com"
} | ConvertTo-Json

Invoke-RestMethod -Uri "http://localhost:8080/api/delete-user" -Method Post -Body $del -ContentType "application/json" -Headers @{"X-API-Key"="YOUR_PRIME_KEY"}
```

---

## 5. Get User Status

Retrieve all details for a specific `licenseId`.

**Endpoint**: `GET /api/credits/:licenseId`

**curl Example**:
```bash
curl -H "X-API-Key: YOUR_PRIME_KEY" http://localhost:8080/api/credits/USER_LICENSE_ID
```

---

## Standalone Infrastructure

This application is now 100% standalone and does NOT require Firebase connectivity. 

- **Local Storage**: Data is saved in `.local/credits_cache.json.enc`.
- **Encryption**: Uses AES-GCM with a 32-byte passphrase.
- **Privacy**: No user data leaves your server.
- **Cross-App Support**: Use the `application` field in each user record to differentiate usage between "EasyAI", "EasyEditor", or your own custom tools.
