FROM golang:1.26-alpine AS build

WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} CGO_ENABLED=0

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/tradeforge-server ./cmd/server \
 && go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate \
 && go build -trimpath -ldflags="-s -w" -o /out/forge ./cmd/forge \
 && go build -trimpath -ldflags="-s -w" -o /out/broker-stub ./cmd/broker-stub

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 forge
COPY --from=build /out/tradeforge-server /out/migrate /out/forge /out/broker-stub /usr/local/bin/

USER forge
EXPOSE 9090 8080 50053 50054

ENTRYPOINT ["tradeforge-server"]
