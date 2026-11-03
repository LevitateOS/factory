module github.com/levitateos/sodaos

go 1.26.7

require (
	forgejo.org/extension-sdk v0.0.0
	github.com/coder/websocket v1.8.15
	github.com/dicebear/dicebear-go/v10 v10.7.0
	github.com/google/go-github/v93 v93.0.0
	github.com/google/jsonschema-go v0.4.3
	github.com/jackc/pgx/v5 v5.10.0
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/sourcegraph/go-diff v0.9.0
	github.com/stretchr/testify v1.12.1
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/crypto v0.57.0
	golang.org/x/sys v0.48.0
)

require (
	github.com/BurntSushi/toml v1.4.1-0.20240526193622-a339e1f7089c // indirect
	github.com/dicebear/schema v1.5.1 // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/hashicorp/go-hclog v1.6.3 // indirect
	github.com/hashicorp/go-plugin v1.8.0 // indirect
	github.com/hashicorp/yamux v0.1.2 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kisielk/errcheck v1.9.0 // indirect
	github.com/mattn/go-colorable v0.1.12 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/oklog/run v1.1.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.49.0
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/grpc v1.83.2 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	honnef.co/go/tools v0.8.1 // indirect
	mvdan.cc/gofumpt v0.9.1 // indirect
)

tool (
	github.com/kisielk/errcheck
	honnef.co/go/tools/cmd/staticcheck
	mvdan.cc/gofumpt
)

replace forgejo.org/extension-sdk => ../forgejo-ext/sdk
