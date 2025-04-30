build:
	go mod tidy
	go build -o dictpress-tts main.go

release:
	goreleaser release --snapshot --clean

run:
	go run main.go

distcheck:
	make build
	./dictpress-tts --help
