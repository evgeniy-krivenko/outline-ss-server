FROM golang:1.18 AS builder

RUN mkdir /app

ADD . /app
WORKDIR /app

RUN CGO_ENABLED=0 GOOS=linux go build -o app main.go

FROM alpine:latest AS prod

RUN mkdir /app
WORKDIR /app

COPY --from=builder /app/app .
CMD [ "./app", "-config", "config.yml", "-metrics", "localhost:9091", "--replay_history=10000"]