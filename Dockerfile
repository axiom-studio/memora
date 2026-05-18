# Memora Core — multi-stage Dockerfile.
# Final image is FROM scratch; produces a ~10MB image carrying just
# the memora-core + memora-cli static binaries.

FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X 'main.version=${VERSION}' -X 'main.commit=${COMMIT}' -X 'main.buildDate=${BUILD_DATE}'" \
    -o /out/memora-core ./cmd/memora-core && \
    CGO_ENABLED=0 go build \
    -ldflags "-s -w -X 'main.version=${VERSION}' -X 'main.commit=${COMMIT}' -X 'main.buildDate=${BUILD_DATE}'" \
    -o /out/memora-cli ./cmd/memora-cli

FROM gcr.io/distroless/static-debian12:nonroot AS final

COPY --from=builder /out/memora-core /usr/local/bin/memora-core
COPY --from=builder /out/memora-cli /usr/local/bin/memora-cli

# Default data directory; mount /data as a volume in production.
ENV MEMORA_DATA_DIR=/data
ENV MEMORA_ADDR=:7777
EXPOSE 7777

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/memora-core"]
CMD ["serve"]
