FROM golang:1.22-alpine AS builder
WORKDIR /app

# copy shared proto module
COPY proto/ ./proto/

# copy service
WORKDIR /app/notification-service
COPY notification-service/go.mod notification-service/go.sum ./
RUN go mod download
COPY notification-service/ .
RUN go build -o notification-service .

FROM alpine:3.21
WORKDIR /app
COPY --from=builder /app/notification-service/notification-service .
EXPOSE 9090
CMD ["./notification-service"]
