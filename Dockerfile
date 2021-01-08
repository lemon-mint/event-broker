FROM golang:alpine as build

RUN apk update
RUN apk add git
WORKDIR /app
RUN git clone https://github.com/lemon-mint/event-broker.git
WORKDIR /app/event-broker
RUN go build -ldflags="-s -w" eventserver.go
EXPOSE 16745
FROM alpine:latest
COPY --from=build /app/event-broker /app/event-broker
WORKDIR /app/event-broker
ENTRYPOINT "/app/event-broker/eventserver"
