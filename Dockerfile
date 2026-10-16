FROM golang:1.24.1-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o blackwatch-bot ./cmd/app/main.go

FROM alpine:latest

RUN apk --no-cache add ca-certificates bash netcat-openbsd tzdata

WORKDIR /root/

COPY --from=builder /app/blackwatch-bot .
COPY --from=builder /app/migrations ./migrations
# FAQ knowledge base (FAQ_FILE_PATH, assets/faq.md); without it the FAQ service
# failed to start in the container.
COPY --from=builder /app/assets/faq.md ./assets/faq.md
COPY wait-for-postgres.sh google-credentials.json* ./

RUN chmod +x wait-for-postgres.sh

CMD ["./wait-for-postgres.sh", "db", "./blackwatch-bot"]
