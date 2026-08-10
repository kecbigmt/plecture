module github.com/kecbigmt/sennit/plugins/channel-server

go 1.25.6

require (
	github.com/kecbigmt/sennit/contracts/channel-protocol v0.0.0
	github.com/mark3labs/mcp-go v0.48.0
)

replace github.com/kecbigmt/sennit/contracts/channel-protocol => ../../contracts/channel-protocol

require (
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/spf13/cast v1.7.1 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
)
