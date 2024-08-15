build:
	@go build -o bin/webrtc

run: build
	@./bin/webrtc

test:
	@go test ./... -v	