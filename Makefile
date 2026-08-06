.PHONY: run build clean test venv

VENV = tools/Scripts/venv
REQ = tools/Scripts/requirements.txt
STAMP = $(VENV)/.installed

build:
	mkdir -p out
	go build -o out/GoWork .

venv: $(STAMP)

$(STAMP): $(REQ)
	@if [ ! -d "$(VENV)" ]; then \
		echo "Creating virtual environment in $(VENV)..."; \
		python3 -m venv $(VENV); \
	fi
	@echo "Installing Python dependencies..."
	@$(VENV)/bin/pip install -r $(REQ)
	@touch $(STAMP)

run:
	go run .

test:
	go test -v ./...

clean:
	rm -rf out $(VENV)
