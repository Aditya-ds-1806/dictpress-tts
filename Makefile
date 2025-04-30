build:
	go build -o dictpress-tts main.go

release:
	goreleaser release --snapshot --clean

run:
	go run main.go
