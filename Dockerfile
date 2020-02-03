FROM golang:latest
COPY . /src
WORKDIR /src
RUN CGO_ENABLED=0 GOOS=linux go build ./cmd/oauth-refresher

FROM scratch
COPY --from=0 /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=0 /src/oauth-refresher .
ENTRYPOINT ["/oauth-refresher"]
