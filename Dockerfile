FROM golang:1.25-alpine AS builder

ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build \
    -ldflags "-X github.com/swilcox/kurokku-esp-server/internal/version.Version=${VERSION}" \
    -o /kurokku-esp-server ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /kurokku-esp-server /usr/local/bin/kurokku-esp-server
COPY web/templates /app/web/templates
WORKDIR /app
EXPOSE 8080
ENTRYPOINT ["kurokku-esp-server"]
