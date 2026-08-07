// Command seed fills Db/gowork.db with the sample usage data from
// Db/insert.sql so the stats tab (code → skills → stats) shows something
// before any real work. It wipes the three runtime tables first, so re-running
// it is safe.
//
// Usage (run from the project root):
//
//	go run ./Db/seed            # re-seed the sample data
//	go run ./Db/seed clear      # empty the db back to just the schema
//
// Close the app first — seeding while it holds the database file open will
// block on the busy timeout and fail.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	"GoWork/Db"

	_ "modernc.org/sqlite"
)

func main() {
	clearOnly := len(os.Args) > 1 && os.Args[1] == "clear"

	h, err := sql.Open("sqlite",
		"file:"+db.DefaultPath+
			"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		fatal(err)
	}
	defer h.Close()

	for _, stmt := range []string{
		"DELETE FROM messages",
		"DELETE FROM usage",
		"DELETE FROM sessions",
	} {
		if _, err := h.Exec(stmt); err != nil {
			fatal(fmt.Errorf("wipe %q: %w", stmt, err))
		}
	}

	if clearOnly {
		fmt.Printf("cleared %s\n", db.DefaultPath)
		return
	}

	data, err := os.ReadFile("Db/insert.sql")
	if err != nil {
		fatal(err)
	}
	// Comment lines are dropped wholesale so their trailing "-- ..." text
	// can't confuse the ';' splitter below.
	var clean []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		clean = append(clean, line)
	}
	for _, stmt := range strings.Split(strings.Join(clean, "\n"), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := h.Exec(stmt); err != nil {
			fatal(fmt.Errorf("seed exec %q: %w", stmt[:min(len(stmt), 40)], err))
		}
	}

	fmt.Printf("seeded %s\n", db.DefaultPath)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "seed:", err)
	os.Exit(1)
}