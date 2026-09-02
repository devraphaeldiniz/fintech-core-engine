FROM golang:alpine AS builder
WORKDIR /app

ENV GOTOOLCHAIN=auto
ENV CGO_ENABLED=0
ENV GOOS=linux

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -ldflags="-w -s" -o /app/fintech-engine ./cmd/api

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /root/
COPY --from=builder /app/fintech-engine .
EXPOSE 8080
CMD ["./fintech-engine"]
