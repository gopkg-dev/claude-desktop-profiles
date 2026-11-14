.PHONY: dev run test vet lint test-native build release

UI_DIR := desktopui

dev:
	go tool nsgo dev -target native

run:
	go tool nsgo build -target macos_arm64 -installers=false -run

test:
	go test -race ./...

vet:
	go vet ./...

lint:
	go tool staticcheck ./...

test-native:
	go tool nsgo test -target macos $(UI_DIR)

build:
	go tool nsgo build -target macos_arm64

# release builds the installer and Sparkle appcast to upload to GitHub Releases. Like the release
# workflow it signs ad hoc, so the published app carries no developer name or email.
release:
	NSGO_UPDATES_KEY="$$(security find-generic-password -s claude-desktop-profiles-updates -w)" \
		go tool nsgo build -target macos_arm64 -sign -
