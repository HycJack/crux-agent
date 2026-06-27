// Module crux-agent-runtime — version v0.0.1
module crux-agent-runtime

go 1.25.0

require github.com/hycjack/crux-ai v0.0.1

require github.com/mattn/go-sqlite3 v1.14.45

require (
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/term v0.44.0 // indirect
)

replace github.com/hycjack/crux-ai => ../crux-ai
