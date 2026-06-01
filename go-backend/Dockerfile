FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/exchange-api ./cmd/api

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/exchange-api /usr/local/bin/exchange-api
RUN mkdir -p /app/data
EXPOSE 8080
ENTRYPOINT ["exchange-api"]
