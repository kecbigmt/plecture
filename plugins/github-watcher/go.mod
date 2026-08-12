module github.com/cradel-dev/cradel/plugins/github-watcher

go 1.25.6

require (
	github.com/cradel-dev/cradel/contracts/atomicfile v0.0.0
	github.com/cradel-dev/cradel/contracts/event v0.0.0
)

replace (
	github.com/cradel-dev/cradel/contracts/atomicfile => ../../contracts/atomicfile
	github.com/cradel-dev/cradel/contracts/event => ../../contracts/event
)
