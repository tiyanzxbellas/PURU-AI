# Build stage
FROM golang:1.26-alpine AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app/puru-ai .

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=build /app/puru-ai .

ENV HOSTNAME=0.0.0.0
EXPOSE 3000

CMD ["/app/puru-ai"]