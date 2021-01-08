FROM golang:latest

WORKDIR /app
RUN git clone https://github.com/lemon-mint/event-broker.git
WORKDIR /app/event-broker
RUN go build eventserver.go
EXPOSE 16745
CMD ./eventserver
