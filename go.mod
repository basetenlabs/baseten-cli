module github.com/basetenlabs/baseten-cli

go 1.26.1

require (
	github.com/basetenlabs/baseten-go v0.0.0-00010101000000-000000000000
	github.com/itchyny/gojq v0.12.18
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.9
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/itchyny/timefmt-go v0.1.7 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// TODO: Remove replace directive once baseten-go has a tagged release
replace github.com/basetenlabs/baseten-go => ../baseten-go
