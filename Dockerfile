# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /workspace
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

# Copy source code
COPY main.go main.go
COPY api/ api/
COPY pkg/ pkg/

# Build operator binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -ldflags="-w -s" -o manager main.go

# Final stage - minimal runtime image
FROM alpine:3.18

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /

# Copy the binary from builder
COPY --from=builder /workspace/manager .

USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/manager"]
