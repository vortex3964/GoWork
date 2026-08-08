.PHONY: run build clean test venv init_db clean_db

VENV = tools/Scripts/venv
REQ = tools/Scripts/requirements.txt
STAMP = $(VENV)/.installed

DB_DIR    = Db
DB_FILE   = $(DB_DIR)/gowork.db
DB_SEED   = $(DB_DIR)/insert.sql
DB_SCHEMA = $(DB_DIR)/schema.sql

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

init_db:
	@command -v sqlite3 >/dev/null 2>&1 || { echo "error: sqlite3 not found on PATH" >&2; exit 1; }
	@mkdir -p $(DB_DIR)
	@rm -f $(DB_FILE)
	@echo "Creating schema in $(DB_FILE)..."
	@sqlite3 $(DB_FILE) < $(DB_SCHEMA)
	@echo "Seeding $(DB_FILE) from $(DB_SEED)..."
	@sqlite3 $(DB_FILE) < $(DB_SEED)
	@echo "Done: $(DB_FILE) seeded."

# clean_db wipes the local sqlite store outright so the next app run starts
# from a fresh, empty database (auto-recreated on open).
clean_db:
	rm -f $(DB_FILE)
	@echo "Removed $(DB_FILE)."
