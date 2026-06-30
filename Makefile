.PHONY: run build clean

build:
	mkdir -p out
	go build -o out/GoWork .

run:
	go run .

clean:
	rm -rf out
