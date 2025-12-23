FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build

WORKDIR /app

# Cache deps
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build binary
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    CGO_ENABLED=0 \
    GOOS=$TARGETOS \
    GOARCH=$TARGETARCH \
    go build -o spotokn ./cmd/spotokn


FROM chromedp/headless-shell

ENV BROWSER_BIN=/headless-shell/headless-shell

COPY --from=build /app/spotokn /usr/local/bin/spotokn

ENTRYPOINT ["/usr/local/bin/spotokn"]
