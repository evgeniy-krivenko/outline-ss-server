FROM golang:1.18 AS builder

RUN mkdir /app

ADD . /app
WORKDIR /app

RUN CGO_ENABLED=0 GOOS=linux go build -o app main.go

FROM alpine:latest AS prod

RUN mkdir /app
WORKDIR /app

COPY --from=builder /app/app .
COPY --from=builder /app/config.yml .
ENTRYPOINT ["./app"]
CMD [ "-config", "config.yml", "-metrics", "localhost:9092", "--replay_history=10000"]