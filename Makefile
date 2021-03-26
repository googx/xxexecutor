.PHONY: build
build:
	# protoc-gen-go 需要使用 github.com/golang/protobuf@v1.3.2 版本
	protoc --proto_path=proto/ --proto_path=protolib/ --go_out=plugins=grpc:pbgocmd/ ./proto/*

	# protoc --proto_path=proto/ --proto_path=../protolib/  --go_out=pbgo_grpc/ ./proto/*
#	protoc --proto_path=proto/ --proto_path=../protolib/ --go_out=pbgo/ ./proto/*
	#protoc --proto_path=proto/ --proto_path=../protolib/ --go_out=plugins=grpc:pbgo_grpc/ commandService.proto

