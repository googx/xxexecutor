module github.com/googx/xxexecutor

go 1.13

require (
	github.com/golang/protobuf v1.4.0
	// github.com/golang/protobuf v1.4.2 why not 1.3.2,原因也是因为版本不匹配的proto-gen-go生成出的pbgo文件导致的
	// google.golang.org/protobuf v1.25.0 why import, 原因是proto-gen-go 生成器使用的是该版本编译的
	github.com/google/go-cmp v0.5.1 // indirect
	github.com/googx/fcommons v0.0.9
	go.uber.org/zap v1.16.0
	google.golang.org/genproto v0.0.0-20200122232147-0452cf42e150 // indirect
	// 1.26版本的grpc和 github.com/golang/protobuf v1.4.3版本的proto-gen-go 不兼容,需要1.27版本的grpc才行,但是1.27版本的grpc和micro框架引用的etcd@v3.3.18不兼容
	// 所以只能 对 github.com/golang/protobuf 版本降级, 选择v1.3.2的proto-gen-go 版本
	google.golang.org/grpc v1.26.0
	google.golang.org/protobuf v1.21.0
)
