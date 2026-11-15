FROM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/yomibako ./cmd/yomibako

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 1000 -h /home/yomibako yomibako

COPY --from=build /out/yomibako /usr/local/bin/yomibako

USER yomibako
WORKDIR /config

ENTRYPOINT ["yomibako"]
CMD ["bot"]
