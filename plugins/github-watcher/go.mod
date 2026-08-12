module github.com/kecbigmt/plect/plugins/github-watcher

go 1.25.6

require (
	github.com/kecbigmt/plect/contracts/atomicfile v0.0.0
	github.com/kecbigmt/plect/contracts/event v0.0.0
)

replace (
	github.com/kecbigmt/plect/contracts/atomicfile => ../../contracts/atomicfile
	github.com/kecbigmt/plect/contracts/event => ../../contracts/event
)
