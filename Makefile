.PHONY: run build clean

build:
	mkdir -p out
	go build -o out/GoWork .

run:
	go run .
test:
	go test -v ./...
clean:
	rm -rf out
