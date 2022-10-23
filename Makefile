generate-proto-api:
	@protoc --go_out=./pkg --go_grpc_out=./pkg ./api/proto/connection.proto

build:
	go build -o ./.bin/ss_server main.go