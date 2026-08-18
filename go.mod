module github.com/tdeshazo/agentflow-spec

go 1.24.0

require gopkg.in/yaml.v3 v3.0.1

require (
	github.com/itchyny/gojq v0.12.17 // indirect
	github.com/itchyny/timefmt-go v0.1.6 // indirect
	github.com/mattn/go-runewidth v0.0.15 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	golang.org/x/sys v0.20.0 // indirect
)

require github.com/mattn/go-isatty v0.0.20

tool (
	github.com/itchyny/gojq/cmd/gojq
	github.com/tdeshazo/agentflow-spec/cmd/jq
)
