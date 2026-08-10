module github.com/kecbigmt/sennit/app

go 1.25.6

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/kecbigmt/sennit/contracts/atomicfile v0.0.0
	github.com/kecbigmt/sennit/contracts/channel-protocol v0.0.0
	github.com/kecbigmt/sennit/contracts/event v0.0.0
	github.com/kecbigmt/sennit/contracts/state v0.0.0
	github.com/mark3labs/mcp-go v0.48.0
	github.com/oklog/ulid/v2 v2.1.0
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2
	github.com/spf13/cobra v1.10.2
)

require (
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/cast v1.7.1 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/text v0.14.0 // indirect
)

replace (
	github.com/kecbigmt/sennit/contracts/atomicfile => ../contracts/atomicfile
	github.com/kecbigmt/sennit/contracts/channel-protocol => ../contracts/channel-protocol
	github.com/kecbigmt/sennit/contracts/event => ../contracts/event
	github.com/kecbigmt/sennit/contracts/state => ../contracts/state
)
