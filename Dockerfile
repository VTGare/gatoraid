FROM golang:1.27 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gatoraid ./cmd/gatoraid
# scratch has no directories. SQLite needs a writable /tmp, and a named volume
# mounted on /data takes this ownership the first time it's used.
RUN mkdir -p /out/data /out/tmp && chown 65532:65532 /out/data && chmod 1777 /out/tmp

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /out/ /
USER 65532:65532
ENV GATORAID_DATABASE_PATH=/data/gatoraid.db
VOLUME /data
ENTRYPOINT ["/gatoraid"]
