APP_NAME := locorum
BUILD_DIR := build/bin

.PHONY: build linux-amd64 darwin-amd64 darwin-arm64 windows-amd64 all clean test

build:
	go build -o $(BUILD_DIR)/$(APP_NAME) .

linux-amd64:
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(APP_NAME)-linux-amd64 .

darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build -o $(BUILD_DIR)/$(APP_NAME)-darwin-amd64 .

darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build -o $(BUILD_DIR)/$(APP_NAME)-darwin-arm64 .

windows-amd64:
	GOOS=windows GOARCH=amd64 go build -o $(BUILD_DIR)/$(APP_NAME)-windows-amd64.exe .

all: linux-amd64 darwin-amd64 darwin-arm64 windows-amd64

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./...
