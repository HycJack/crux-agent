// Module crux-agent-chat — version v0.0.1
module crux-agent-chat

go 1.25.0

require (
	github.com/hycjack/agent-engine v0.0.1
	github.com/hycjack/crux-ai v0.0.1
	github.com/joho/godotenv v1.5.1
	golang.org/x/term v0.44.0
)

require (
	github.com/dlclark/regexp2 v1.11.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/pkoukk/tiktoken-go v0.1.6 // indirect
	golang.org/x/sys v0.46.0 // indirect
)

replace (
	github.com/hycjack/agent-engine => ../agent-engine
	github.com/hycjack/crux-ai => ../crux-ai
)
