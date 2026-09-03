FROM golang:1.22-bookworm AS rpc-builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/grid-remote-shell .

FROM eclipse-temurin:21-jre

RUN apt-get update -qq \
    && apt-get install -y -qq ca-certificates curl procps \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 10001 --shell /bin/sh beam \
    && install -d -o beam -g beam /var/lib/somewear/beam /home/beam/.config/somewear

COPY --from=rpc-builder /out/grid-remote-shell /usr/local/bin/grid-remote-shell
COPY docker-harness/entrypoint.sh /usr/local/bin/grid-harness-entrypoint

RUN chmod 755 /usr/local/bin/grid-remote-shell /usr/local/bin/grid-harness-entrypoint

USER beam
WORKDIR /home/beam

ENV BEAM_IS_JAR=true
ENV BEAM_IS_DAEMON=true
ENV LOG_LEVEL=info

ENTRYPOINT ["/usr/local/bin/grid-harness-entrypoint"]
