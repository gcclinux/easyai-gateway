# Build the Go Binary
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache gcc musl-dev

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o main .

# Create the minimal runtime image
FROM alpine:latest

WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/main .

# Copy static files and templates
COPY static/ ./static/
COPY templates/ ./templates/

# Ensure the app binds to the port provided by Cloud Run
ENV SERVER_PORT=8080
ENV SERVER_HOST=0.0.0.0

EXPOSE 8080

# Run the application
CMD ["./main"]
