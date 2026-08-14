module github.com/kecbigmt/plect/plugins/legacy-migration

go 1.25.6

require github.com/BurntSushi/toml v1.6.0

require github.com/kecbigmt/plect/contracts/state v0.0.0

replace github.com/kecbigmt/plect/contracts/state => ../../contracts/state
