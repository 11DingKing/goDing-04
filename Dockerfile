FROM golang:1.26 AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/arcticfreight ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/arcticfreight /app/arcticfreight
RUN mkdir -p /data
EXPOSE 51108
ENV ARCTICFREIGHT_DATA_PATH=/data/state.json
ENTRYPOINT ["/app/arcticfreight"]
