FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
COPY internal/ ./internal/
COPY web/ ./web/
COPY examples/ ./examples/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/trail-conditions .
RUN mkdir -p /out/data && chown 65532:65532 /out/data

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/trail-conditions /trail-conditions
COPY --from=build --chown=65532:65532 /out/data /data
ENV TRAIL_LISTEN_ADDR=0.0.0.0:8080
ENV TRAIL_DATA_DIR=/data
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/trail-conditions"]
