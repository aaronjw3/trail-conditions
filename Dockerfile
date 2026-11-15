FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
COPY internal/ ./internal/
COPY web/ ./web/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/trail-conditions .

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/trail-conditions /trail-conditions
ENV TRAIL_LISTEN_ADDR=0.0.0.0:8080
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/trail-conditions"]
