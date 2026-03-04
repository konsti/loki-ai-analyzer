FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /analyzer ./cmd/analyzer

FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata

COPY --from=builder /analyzer /usr/local/bin/analyzer
COPY prompts/ /etc/analyzer/prompts/

ENTRYPOINT ["analyzer"]
