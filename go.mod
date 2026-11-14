module claudeprofiles

go 1.27.3

require (
	github.com/ebitengine/purego v0.11.3
	github.com/imroc/req/v3 v3.61.0
	github.com/nsgo-dev/nsgo v0.0.0-20261114154133-a4dcdc0ba394
	github.com/refraction-networking/utls v1.8.2
	golang.org/x/sys v0.50.0
)

require (
	github.com/BurntSushi/toml v1.4.1-0.20240526193622-a339e1f7089c // indirect
	github.com/andybalholm/brotli v1.3.0 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/icholy/digest v1.2.0 // indirect
	github.com/klauspost/compress v1.20.3 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.61.0 // indirect
	github.com/yuin/goldmark v1.8.7 // indirect
	go.yaml.in/yaml/v3 v3.0.6 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/image v0.48.0 // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/text v0.44.0 // indirect
	golang.org/x/tools v0.52.0 // indirect
	honnef.co/go/tools v0.8.2 // indirect
)

tool (
	claudeprofiles/tools/nsgo
	honnef.co/go/tools/cmd/staticcheck
)
