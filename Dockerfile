# syntax=docker/dockerfile:1

# Build stage
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/email-mcp ./cmd/email-mcp

# Runtime stage: distroless static (Debian 13, latest) has CA certificates
# (needed for TLS to IMAP/SMTP servers) and runs as nonroot.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/email-mcp /email-mcp
ENV EMAIL_MCP_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/email-mcp"]
