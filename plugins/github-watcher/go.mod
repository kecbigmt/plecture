module github.com/plecture/plect/plugins/github-watcher

go 1.25.6

require (
	github.com/plecture/plect/contracts/atomicfile v0.0.0
	github.com/plecture/plect/contracts/event v0.0.0
)

replace (
	github.com/plecture/plect/contracts/atomicfile => ../../contracts/atomicfile
	github.com/plecture/plect/contracts/event => ../../contracts/event
)
