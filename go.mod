module github.com/tdeshazo/agentflow

go 1.24.0

require (
	github.com/mattn/go-isatty v0.0.20
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/google/go-cmp v0.5.4 // indirect
	github.com/itchyny/gojq v0.12.17 // indirect
	github.com/itchyny/timefmt-go v0.1.6 // indirect
	github.com/mattn/go-runewidth v0.0.15 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/sys v0.20.0 // indirect
	gopkg.in/check.v1 v0.0.0-20161208181325-20d25e280405 // indirect
)

tool (
	github.com/itchyny/gojq/cmd/gojq
	github.com/tdeshazo/agentflow/cmd/jq
)
