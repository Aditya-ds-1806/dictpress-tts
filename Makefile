build:
	go mod tidy
	go build -o dictpress-tts ./cmd

release:
	goreleaser release --snapshot --clean

run:
	go run cmd/*.go

distcheck:
	make build
	./dictpress-tts --help
