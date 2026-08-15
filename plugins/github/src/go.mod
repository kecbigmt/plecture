module github.com/kecbigmt/plecture/plugins/github/src

go 1.25.6

require (
	github.com/kecbigmt/plecture/contracts/atomicfile v0.0.0
	github.com/kecbigmt/plecture/contracts/event v0.0.0
)

replace (
	github.com/kecbigmt/plecture/contracts/atomicfile => ../../../contracts/atomicfile
	github.com/kecbigmt/plecture/contracts/event => ../../../contracts/event
)
