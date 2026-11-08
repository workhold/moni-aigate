FROM node:22-alpine AS console
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS api
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=console /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /moni-aigate ./cmd/moni-aigate

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata && addgroup -S moni && adduser -S -G moni moni
COPY --from=api /moni-aigate /usr/local/bin/moni-aigate
USER moni
WORKDIR /app
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["moni-aigate"]
CMD ["serve"]
