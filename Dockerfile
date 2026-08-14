# Frontend build stage (Vite + React for the /login settings page).
# The bundle is written to ../internal/web/dist and embedded into the Go binary.
FROM node:22-alpine AS web-build

WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

# Go build stage
FROM golang:1.26-alpine AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Use the freshly built frontend bundle from the web stage (keeps the image
# self-contained even when internal/web/dist is not committed).
COPY --from=web-build /web/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app/puru-ai .

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=build /app/puru-ai .

# Bind-all is fine for the web server; the /login link never uses this value
# as a public host (config.ResolvePublicBaseURL skips 0.0.0.0/::/empty).
ENV HOSTNAME=0.0.0.0
EXPOSE 3000

CMD ["/app/puru-ai"]
