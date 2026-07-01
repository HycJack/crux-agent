// Module crux-agent-chat — version v0.0.1
module crux-agent-chat

go 1.25.0

require (
	crux-agent-harness v0.0.1
	crux-agent-runtime v0.0.1
	github.com/hycjack/crux-ai v0.0.1
	github.com/joho/godotenv v1.5.1
	golang.org/x/term v0.44.0
)

require (
	github.com/dlclark/regexp2 v1.10.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v0.1.9 // indirect
	github.com/pkoukk/tiktoken-go v0.1.6 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/exp v0.0.0-20250408133849-7e4ce0ab07d0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	modernc.org/libc v1.65.7 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.37.1 // indirect
)

replace (
	crux-agent-harness => ../crux-agent-harness
	crux-agent-runtime => ../crux-agent-runtime
	github.com/hycjack/crux-ai => ../crux-ai
)
