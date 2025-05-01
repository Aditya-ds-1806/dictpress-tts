build:
	go mod tidy
	go build -o dictpress-tts

release:
	goreleaser release --snapshot --clean

run:
	go run main.go version.go

distcheck:
	make build
	./dictpress-tts --help
