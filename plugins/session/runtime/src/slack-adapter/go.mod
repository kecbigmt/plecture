module github.com/kecbigmt/plecture/plugins/session/runtime/src/slack-adapter

go 1.25.6

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/kecbigmt/plecture/contracts/channel-protocol v0.0.0
	github.com/kecbigmt/plecture/contracts/event v0.0.0
	github.com/kecbigmt/plecture/plugins/session/runtime/src/channel-server v0.0.0
	github.com/slack-go/slack v0.22.0
)

require (
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/mark3labs/mcp-go v0.48.0 // indirect
	github.com/spf13/cast v1.7.1 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
)

replace (
	github.com/kecbigmt/plecture/contracts/channel-protocol => ../../../../../contracts/channel-protocol
	github.com/kecbigmt/plecture/contracts/event => ../../../../../contracts/event
	github.com/kecbigmt/plecture/plugins/session/runtime/src/channel-server => ../channel-server
)
