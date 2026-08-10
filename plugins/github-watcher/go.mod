module github.com/kecbigmt/sennit/plugins/github-watcher

go 1.25.6

require (
	github.com/kecbigmt/sennit/contracts/atomicfile v0.0.0
	github.com/kecbigmt/sennit/contracts/event v0.0.0
)

replace (
	github.com/kecbigmt/sennit/contracts/atomicfile => ../../contracts/atomicfile
	github.com/kecbigmt/sennit/contracts/event => ../../contracts/event
)
