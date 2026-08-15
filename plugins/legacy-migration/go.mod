module github.com/kecbigmt/plecture/plugins/legacy-migration

go 1.25.6

require github.com/BurntSushi/toml v1.6.0

require github.com/kecbigmt/plecture/contracts/state v0.0.0

replace github.com/kecbigmt/plecture/contracts/state => ../../contracts/state
