module github.com/altessa-s/go-aip/backend/sqlbase

go 1.27.0

toolchain go1.27.3

require (
	github.com/altessa-s/go-aip v0.1.1
	github.com/altessa-s/go-aip/backend/clickhouse v0.1.1
	github.com/altessa-s/go-aip/backend/mariadb v0.1.1
	github.com/altessa-s/go-aip/backend/postgres v0.1.1
	github.com/altessa-s/go-atlas-core v0.3.3
	github.com/shopspring/decimal v1.4.0
	github.com/stretchr/testify v1.12.1
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260818201246-1b0934165a6f
	google.golang.org/protobuf v1.36.12
)

require (
	cel.dev/cel-go v0.33.2 // indirect
	cel.dev/expr v0.25.1 // indirect
	github.com/altessa-s/go-kryptos v0.1.4 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/exp v0.0.0-20240823005443-9b4947da3948 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260819154853-08b0e4226688 // indirect
)

replace (
	github.com/altessa-s/go-aip => ../..
	github.com/altessa-s/go-aip/backend/clickhouse => ../clickhouse
	github.com/altessa-s/go-aip/backend/mariadb => ../mariadb
	github.com/altessa-s/go-aip/backend/postgres => ../postgres
)
