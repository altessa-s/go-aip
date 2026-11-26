module github.com/altessa-s/go-aip/backend/mongo

go 1.27.0

toolchain go1.27.4

require (
	github.com/altessa-s/go-aip v0.1.15
	github.com/altessa-s/go-atlas-core v0.3.5
	github.com/altessa-s/go-mongox v0.1.2
	github.com/shopspring/decimal v1.4.0
	github.com/stretchr/testify v1.12.1
	go.mongodb.org/mongo-driver/v2 v2.10.1
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261012220534-4341edf3f941
	google.golang.org/protobuf v1.36.12
)

require (
	cel.dev/cel-go v0.33.2 // indirect
	cel.dev/expr v0.25.1 // indirect
	github.com/altessa-s/go-kryptos v0.1.5 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/klauspost/compress v1.20.3 // indirect
	github.com/xdg-go/scram v1.4.0 // indirect
	github.com/xdg-go/stringprep v1.0.4 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/exp v0.0.0-20240823005443-9b4947da3948 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20261019180522-ce908d29299b // indirect
)

replace github.com/altessa-s/go-aip => ../..
