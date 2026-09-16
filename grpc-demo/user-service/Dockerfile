FROM golang:1.22-alpine AS builder
WORKDIR /app

# copy shared proto module
COPY proto/ ./proto/

# copy service
WORKDIR /app/user-service
COPY user-service/go.mod user-service/go.sum ./
RUN go mod download
COPY user-service/ .
RUN go build -o user-service .

FROM alpine:3.21
WORKDIR /app
COPY --from=builder /app/user-service/user-service .
EXPOSE 8080 50051
CMD ["./user-service"]
