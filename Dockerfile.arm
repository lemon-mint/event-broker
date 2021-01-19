FROM golang:alpine as build

RUN apk update
RUN apk add git
ADD . /app
WORKDIR /app
RUN go build -ldflags="-s -w" -v eventserver.go

FROM alpine:latest
COPY --from=build /app /app
EXPOSE 16745
WORKDIR /app
CMD /app/eventserver
