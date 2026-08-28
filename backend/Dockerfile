FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /paas ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates docker-cli docker-cli-compose openssh-client
COPY --from=build /paas /usr/local/bin/paas
USER root
ENTRYPOINT ["paas"]
