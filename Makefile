# Makefile for yagent

.PHONY: build test run clean

build:
	go build -o yagent .

test:
	go test ./...

run: build
	./yagent

clean:
	rm -f yagent