FROM golang:1.21-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o node-lifecycle-controller ./cmd/controller

FROM alpine:3.19

RUN apk --no-cache add ca-certificates

WORKDIR /

COPY --from=builder /app/node-lifecycle-controller .

USER 65532:65532

ENTRYPOINT ["/node-lifecycle-controller"]
