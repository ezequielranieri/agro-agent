# syntax=docker/dockerfile:1

# --- Stage 1: build the static Go binary ----------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /app

# Cache module downloads independently of source changes.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the module and compile the API.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/api ./cmd/api

# --- Stage 2: minimal runtime image ----------------------------------------
FROM alpine:3.20

# CA certificates are required for the Gemini HTTPS API calls.
RUN apk add --no-cache ca-certificates

# Run as a non-root user.
RUN addgroup -S app && adduser -S app -G app

COPY --from=build /app/api /app/api

USER app
EXPOSE 8080

ENTRYPOINT ["/app/api"]
