FROM golang:1.24.1-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o valhalla-bot ./cmd/app/main.go

FROM alpine:latest

RUN apk --no-cache add ca-certificates bash

WORKDIR /root/

COPY --from=builder /app/valhalla-bot .

COPY --from=builder /app/migrations ./migrations

COPY wait-for-postgres.sh .
RUN chmod +x wait-for-postgres.sh

CMD ["./wait-for-postgres.sh", "db", "./valhalla-bot"]