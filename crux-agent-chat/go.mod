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

require golang.org/x/sys v0.46.0 // indirect

replace (
	crux-agent-harness => ../crux-agent-harness
	crux-agent-runtime => ../crux-agent-runtime
	github.com/hycjack/crux-ai => ../crux-ai
)
